package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/ko/pkg/build"
	"github.com/google/ko/pkg/publish"
	"github.com/lema-ai/ippon/registry"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"golang.org/x/sync/errgroup"
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
	verbose      = false
	outputBuffer bytes.Buffer // easier debugging in case of errors, buffer to store output when running in non verbose mode
)

func buildRegistryCommand(cmdName string, registry Registry, servicesConfig ServicesConfig) (*cobra.Command, error) {
	ctx := context.Background()
	maxProcs := viper.GetInt("GO_BUILD_MAX_PROCS")
	if maxProcs == 0 {
		maxProcs = 5
	}
	registryCmd := &cobra.Command{
		Use:  cmdName,
		Args: cobra.MinimumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return registry.Init(ctx)
		},
	}

	releaseCmd := &cobra.Command{
		Use:   "release",
		Short: "Build, tag and push an image",
		Run: func(cmd *cobra.Command, args []string) {
			authOption := getRegistryAuthOption(registry)

			var g errgroup.Group
			g.SetLimit(maxProcs)
			for _, service := range servicesConfig.Services {
				service := service
				g.Go(func() error {
					log.Printf("ippon building service: %+v\n", service)
					baseURL := registry.URL()
					tags := service.GetTags()
					baseImage := service.GetBaseImage()

					err := buildAndPublishService(ctx, service.Main, service.Name, baseURL, baseImage, tags, authOption)
					if err != nil {
						return errors.Wrap(err, "build and push service")
					}

					return nil
				})

			}
			if err := g.Wait(); err != nil {
				finishWithError("fatal error while building service", err)
			}

		},
	}

	registryCmd.AddCommand(releaseCmd)

	if createRepo, ok := registry.(CreateRepoRegistry); ok {
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

func buildAndPublishService(ctx context.Context, cmdDir, serviecName, baseURL, baseImage string, tags []string, authOption publish.Option) error {
	b, err := build.NewGo(ctx, cmdDir,
		build.WithPlatforms("linux/amd64"),
		build.WithDisabledSBOM(),
		build.WithBaseImages(func(ctx context.Context, _ string) (name.Reference, build.Result, error) {
			ref, err := name.ParseReference(baseImage)
			if err != nil {
				return nil, nil, err
			}
			base, err := remote.Index(ref, remote.WithContext(ctx))
			return ref, base, err
		}),
	)

	if err != nil {
		return errors.Wrap(err, "build go image")
	}

	r, err := b.Build(ctx, "")
	if err != nil {
		return errors.Wrap(err, "build image")
	}

	digest, err := r.Digest()
	if err != nil {
		return errors.Wrap(err, "get image digest")
	}

	digestTag := strings.TrimPrefix(digest.String(), "sha256:")
	tags = append(tags, digestTag)

	p, err := publish.NewDefault(baseURL,
		publish.WithTags(tags),
		authOption,
	)
	if err != nil {
		return errors.Wrap(err, "authenticate to image repo")
	}

	ref, err := p.Publish(ctx, r, serviecName)
	if err != nil {
		return errors.Wrap(err, "publish image")
	}

	log.Println(ref.String())
	return nil
}

func getRegistryAuthOption(registry Registry) publish.Option {
	if authReg, ok := registry.(SelfAuthRegistry); ok {
		return authReg.GetAuthOption()
	}
	// use credentials from ~/.docker/config.json.
	log.Println("Using the default docker config.json credentials for login")
	return publish.WithAuthFromKeychain(authn.DefaultKeychain)
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
	pflag.BoolVar(&verbose, "verbose", false, "verbose output")

	viper.SetConfigName(configFileName)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.SetDefault("base_image", defaultBaseImage)
	viper.SetEnvPrefix(configEnvPrefix)
	viper.AutomaticEnv()

	viper.BindPFlag("verbose", pflag.Lookup("v"))
}

func main() {
	err := viper.ReadInConfig()
	if err != nil {
		finishWithError("fatal error config file", err)
	}
	pflag.Parse()

	if !verbose {
		log.SetOutput(&outputBuffer)
	}

	var services ServicesConfig
	err = viper.Unmarshal(&services)
	if err != nil {
		finishWithError("failed getting services from config", err)
	}

	okteto := new(registry.Okteto)
	oktetoCommand, err := buildRegistryCommand("okteto", okteto, services)
	if err != nil {
		finishWithError("failed creating okteto command", err)
	}

	accountId := viper.GetString("ecr.account")
	region := viper.GetString("ecr.region")
	ecr := registry.NewECR(accountId, region)
	ecrCommand, err := buildRegistryCommand("ecr", ecr, services)
	if err != nil {
		finishWithError("failed creating ecr command", err)
	}

	rootCmd := &cobra.Command{
		Use:   "ippon",
		Short: "Ippon build and release Go images",
		Long:  "Ippon make it easy to handle Go images release in a micro-services architecture",
	}

	rootCmd.AddCommand(oktetoCommand, ecrCommand)
	err = rootCmd.Execute()
	if err != nil {
		finishWithError("failed executing command", err)
	}
}
