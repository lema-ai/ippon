package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/ko/pkg/build"
	"github.com/google/ko/pkg/publish"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	baseImage       = "cgr.dev/chainguard/static:latest"
	configFileName  = "ippon"
	configEnvPrefix = "IPPON"
)

var (
	isOktetoContext = false
)

type ServicesConfig struct {
	Services []ServiceConfig `mapstructure:"services"`
}

type ServiceConfig struct {
	Name     string   `mapstructure:"name"`
	Main     string   `mapstructure:"main"`
	Registry string   `mapstructure:"registry"`
	Tags     []string `mapstructure:"tags"`
}

func (this ServiceConfig) GetRegistry() (string, error) {
	// Ignore configs on Okteto context
	if isOktetoContext {
		registryUrl, exists := os.LookupEnv("OKTETO_REGISTRY_URL")
		if !exists {
			return "", errors.New("Failed getting Okteto's registry: OKTETO_REGISTRY_URL not set")
		}

		namespace, exists := os.LookupEnv("OKTETO_NAMESPACE")
		if !exists {
			return "", errors.New("Failed getting Okteto's registry: OKTETO_NAMESPACE not set")
		}

		return fmt.Sprintf("%s/%s", registryUrl, namespace), nil
	}

	if this.Registry != "" {
		return this.Registry, nil
	}

	return viper.GetString("registry.ecr"), nil
}

func (this ServiceConfig) GetTags() []string {
	if this.Tags != nil {
		return this.Tags
	}

	return viper.GetStringSlice("tags")
}

func init() {
	pflag.BoolVar(&isOktetoContext, "okteto", false, "in Okteto context")

	viper.SetConfigName(configFileName)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.SetEnvPrefix(configEnvPrefix)
	viper.AutomaticEnv()

	viper.BindPFlag("okteto", pflag.Lookup("okteto"))
}

// TODO: build it with cobra
func main() {
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatalf("fatal error config file: %v\n", err)
	}
	pflag.Parse()

	var services ServicesConfig
	err = viper.Unmarshal(&services)
	if err != nil {
		log.Fatalf("failed getting services from config: %v\n", err)
	}

	ctx := context.Background()
	for _, service := range services.Services {
		fmt.Printf("building service: %+v\n", service)

		registry, err := service.GetRegistry()
		if err != nil {
			log.Fatalf("failed getting container registry: %v", err)
		}

		tags := service.GetTags()

		err = buildAndPublishService(ctx, service.Main, service.Name, registry, tags)
		if err != nil {
			log.Fatalf("failed to build service: %v\n", err)
		}
	}
}

func buildAndPublishService(ctx context.Context, cmdDir, serviecName, repo string, tags []string) error {
	b, err := build.NewGo(ctx, cmdDir,
		build.WithPlatforms("linux/amd64"),
		build.WithDisabledSBOM(),
		build.WithBaseImages(func(ctx context.Context, _ string) (name.Reference, build.Result, error) {
			ref := name.MustParseReference(baseImage)
			base, err := remote.Index(ref, remote.WithContext(ctx))
			return ref, base, err
		}),
	)

	if err != nil {
		return err
	}

	r, err := b.Build(ctx, "")
	if err != nil {
		return err
	}

  digest, err := r.Digest()
  if err != nil {
    return err
  }

  digestTag := strings.TrimPrefix(digest.String(), "sha256:")
  tags = append(tags, digestTag)

	p, err := publish.NewDefault(repo,
		publish.WithTags(tags),
		getAuthOption(),
	)

	if err != nil {
		return err
	}
	ref, err := p.Publish(ctx, r, serviecName)
	if err != nil {
		return err
	}

	fmt.Println(ref.String())
	return nil
}

func getAuthOption() publish.Option {
	if isOktetoContext {
		return publish.WithAuth(&authn.Basic{
			Username: os.Getenv("OKTETO_USERNAME"),
			Password: os.Getenv("OKTETO_TOKEN"),
		})
	}

	// use credentials from ~/.docker/config.json.
	return publish.WithAuthFromKeychain(authn.DefaultKeychain)

}
