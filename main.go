package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/lema-ai/ippon/pkg/build"
	"github.com/lema-ai/ippon/pkg/build/ko"
	"github.com/lema-ai/ippon/pkg/registry"
	"github.com/lema-ai/ippon/pkg/registry/ecr"
	"github.com/lema-ai/ippon/pkg/registry/okteto"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
)

const (
	defaultBaseImage = "cgr.dev/chainguard/busybox:latest"
	configFileName   = "ippon"
	configEnvPrefix  = "IPPON"
)

var (
	verbose      = false
	outputBuffer bytes.Buffer // easier debugging in case of errors, buffer to store output when running in non verbose mode
)

func tryCallParentPersistentPreRun(cmd *cobra.Command, args []string) error {
	if parent := cmd.Parent(); parent != nil {
		if parent.PersistentPreRunE != nil {
			return parent.PersistentPreRunE(parent, args)
		}
	}
	return nil
}

func buildRegistryCommand(cmdName string, reg registry.Registry, servicesConfig ServicesConfig) (*cobra.Command, error) {
	ctx := context.Background()
	registryCmd := &cobra.Command{
		Use:  cmdName,
		Args: cobra.MinimumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := tryCallParentPersistentPreRun(cmd, args)
			if err != nil {
				return errors.Wrap(err, "failed calling persistent pre run e on parent command")
			}
			return reg.Init(ctx)
		},
	}

	releaseCmd := &cobra.Command{
		Use:   "release",
		Short: "Build, tag and push an image",
		RunE: func(cmd *cobra.Command, args []string) error {
			maxGoRoutines, err := cmd.Flags().GetInt("max-go-routines")
			if err != nil {
				return errors.Wrap(err, "failed getting max-go-routines flag")
			}

			var g errgroup.Group
			g.SetLimit(maxGoRoutines)
			for _, service := range servicesConfig.Services {
				service := service
				g.Go(func() error {
					log.Printf("ippon building service: %+v\n", service)

					buildOpts := build.BuildOptions{
						Platform: []string{"linux/amd64"},
					}

					publishOpts := build.PublishOptions{
						ImageName: service.GetBaseImage(),
						Tags:      service.GetTags(),
					}

					koBuilder := ko.NewBuilder(service.Name, service.GetBaseImage())
					publisher, err := koBuilder.Build(ctx, buildOpts)
					if err != nil {
						return errors.Wrap(err, "build service")
					}

					err = publisher.Publish(ctx, reg, publishOpts)
					if err != nil {
						return errors.Wrap(err, "publish service")
					}

					return nil
				})

			}
			if err := g.Wait(); err != nil {
				return errors.Wrap(err, "fatal error while building service")
			}
			return nil
		},
	}
	releaseCmd.Flags().Int("max-go-routines", 5, "Maximum number of go routines to use for building and pushing images concurrently. Default is 5.")

	registryCmd.AddCommand(releaseCmd)

	if createRepo, ok := reg.(registry.CreateRepoRegistry); ok {
		createMissingCmd := &cobra.Command{
			Use:   "create-missing-repos",
			Short: "Create required and missing repositories in the registry",
			RunE: func(cmd *cobra.Command, args []string) error {
				for _, s := range servicesConfig.Services {
					repo := s.Name
					exists, err := createRepo.RepositoryExists(ctx, repo)
					if err != nil {
						return err
					}

					if !exists {
						err := createRepo.CreateRepository(ctx, repo)
						if err != nil {
							return err
						}
						log.Printf("repository created in registry: %s\n", repo)
					}
				}
				return nil
			},
		}
		registryCmd.AddCommand(createMissingCmd)
	}

	return registryCmd, nil
}

type ServicesConfig struct {
	Services []ServiceConfig `mapstructure:"services"`
}

type ServiceConfig struct {
	Name      string   `mapstructure:"name"`
	Main      string   `mapstructure:"main"`
	Tags      []string `mapstructure:"tags"`
	BaseImage string   `mapstructure:"base-image"`
}

func (this ServiceConfig) GetTags() []string {
	if this.Tags != nil {
		return this.Tags
	}

	return viper.GetStringSlice("tags")
}

func (this ServiceConfig) GetBaseImage() string {
	if this.BaseImage != "" {
		return this.BaseImage
	}

	return viper.GetString("base_image")
}

func finishWithError(msg string, err error) {
	fmt.Print(outputBuffer.String())
	log.SetOutput(os.Stdout)
	log.Fatalf("%s: %v\n", msg, err)
}

func init() {
	viper.SetConfigName(configFileName)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.SetDefault("base-image", defaultBaseImage)
	viper.SetEnvPrefix(configEnvPrefix)
	viper.AutomaticEnv()
}

func main() {
	err := viper.ReadInConfig()
	if err != nil {
		finishWithError("fatal error config file", err)
	}

	var services ServicesConfig
	err = viper.Unmarshal(&services)
	if err != nil {
		finishWithError("failed getting services from config", err)
	}

	oktetoReg := new(okteto.Registry)
	oktetoCommand, err := buildRegistryCommand("okteto", oktetoReg, services)
	if err != nil {
		finishWithError("failed creating okteto command", err)
	}

	accountId := viper.GetString("ecr.account")
	region := viper.GetString("ecr.region")
	ecrReg := ecr.NewECR(accountId, region)
	ecrCommand, err := buildRegistryCommand("ecr", ecrReg, services)
	if err != nil {
		finishWithError("failed creating ecr command", err)
	}

	rootCmd := &cobra.Command{
		Use:   "ippon",
		Short: "Ippon build and release Go images",
		Long:  "Ippon make it easy to handle Go images release in a micro-services architecture",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			verbose, err := cmd.Flags().GetBool("verbose")
			if err != nil {
				return errors.Wrap(err, "failed getting verbose flag")
			}
			if verbose {
				log.SetOutput(os.Stdout)
			} else {
				log.SetOutput(io.Discard)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().Bool("verbose", false, "verbose output")

	rootCmd.AddCommand(oktetoCommand, ecrCommand)
	err = rootCmd.Execute()
	if err != nil {
		finishWithError("failed executing command", err)
	}
}
