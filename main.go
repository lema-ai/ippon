package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/google/ko/pkg/publish"
	"github.com/lema-ai/ippon/registry"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type Registry interface {
	Init(context.Context) error
	URL() string
}

type CreateRepoRegistry interface {
	Registry
	RepositoryExists(ctx context.Context, repo string) (bool, error)
	CreateRepository(ctx context.Context, repo string) error
}

type SelfAuthRegistry interface {
	Registry
	GetAuthOption() publish.Option
}

const (
	defaultBaseImage = "cgr.dev/chainguard/busybox:latest"
	configFileName   = "ippon"
	configEnvPrefix  = "IPPON"
)

var (
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

func buildRegistryCommand(cmdName string, registry *registry.ECR, servicesConfig *ServicesConfig) (*cobra.Command, error) {
	ctx := context.Background()
	registryCmd := &cobra.Command{
		Use:  cmdName,
		Args: cobra.MinimumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			err := tryCallParentPersistentPreRun(cmd, args)
			if err != nil {
				return errors.Wrap(err, "failed calling persistent pre run e on parent command")
			}
			return registry.Init(ctx)
		},
	}

	releaseCmd := &cobra.Command{
		Use:   "release",
		Short: "Build, tag and push an image",
		RunE: func(cmd *cobra.Command, args []string) error {
			return registryCommand(ctx, cmd, args, servicesConfig, registry)
		},
	}
	releaseCmd.Flags().Int("max-go-routines", 5, "Maximum number of go routines to use for building and pushing images concurrently. Default is 5.")
	releaseCmd.Flags().String("namespace", "", "Okteto namespace to update the kustomization file with the new image digests")
	registryCmd.AddCommand(releaseCmd)

	createMissingCmd := &cobra.Command{
		Use:   "create-missing-repos",
		Short: "Create required and missing repositories in the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return createMissingReposCommand(ctx, cmd, args, servicesConfig, registry)
		},
	}
	createMissingCmd.Flags().String("namespace", "", "Okteto namespace to use for the missing repositories")
	registryCmd.AddCommand(createMissingCmd)

	return registryCmd, nil
}

type ServicesConfig struct {
	Services []ServiceConfig `mapstructure:"services"`
}

type ServiceConfig struct {
	Name      string   `mapstructure:"name"`
	Main      string   `mapstructure:"main"`
	Tags      []string `mapstructure:"tags"`
	BaseImage string   `mapstructure:"base_image"`
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
	viper.SetDefault("base_image", defaultBaseImage)
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

	oktetoAccountId := viper.GetString("okteto.account")
	oktetoRegion := viper.GetString("okteto.region")
	devRegistry := registry.NewECR(oktetoAccountId, oktetoRegion)
	oktetoCommand, err := buildRegistryCommand("okteto", devRegistry, &services)
	if err != nil {
		finishWithError("failed creating okteto command", err)
	}

	prodAccountId := viper.GetString("ecr.account")
	prodRegion := viper.GetString("ecr.region")
	prodRegistry := registry.NewECR(prodAccountId, prodRegion)
	ecrCommand, err := buildRegistryCommand("release", prodRegistry, &services)
	if err != nil {
		finishWithError("failed creating release command", err)
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
