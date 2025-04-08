package main

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/ko/pkg/build"
	"github.com/google/ko/pkg/publish"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func buildAndPublishGoService(ctx context.Context, cmdDir, serviceName, baseURL, baseImage, namespace string, tags []string, publishAuthOption publish.Option, remoteAuthOption remote.Option) (*Image, error) {
	b, err := build.NewGo(ctx, cmdDir,
		build.WithPlatforms("linux/amd64"),
		build.WithDisabledSBOM(),
		build.WithBaseImages(func(ctx context.Context, _ string) (name.Reference, build.Result, error) {
			baseImage = strings.ReplaceAll(baseImage, "BASE_URL", baseURL)
			ref, err := name.ParseReference(baseImage)
			if err != nil {
				return nil, nil, err
			}
			base, err := remote.Index(ref, remote.WithContext(ctx), remoteAuthOption)
			return ref, base, err
		}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "build go image")
	}

	r, err := b.Build(ctx, "")
	if err != nil {
		return nil, errors.Wrap(err, "build image")
	}

	digest, err := r.Digest()
	if err != nil {
		return nil, errors.Wrap(err, "get image digest")
	}

	p, err := publish.NewDefault(baseURL,
		publish.WithTags(tags),
		publishAuthOption,
	)
	if err != nil {
		return nil, errors.Wrap(err, "authenticate to image repo")
	}

	repoName := serviceName
	if namespace != "" {
		repoName = path.Join(namespace, serviceName)
	}

	c, err := publish.NewCaching(p)
	if err != nil {
		return nil, errors.Wrap(err, "create caching publisher")
	}

	ref, err := c.Publish(ctx, r, repoName)
	if err != nil {
		return nil, errors.Wrap(err, "publish image")
	}

	return &Image{
		OldName: fmt.Sprintf("registry.lema.ai/%s", serviceName),
		NewName: fmt.Sprintf("%s@%s", ref.Context().Name(), digest),
	}, nil
}

func registryCommand(ctx context.Context, cmd *cobra.Command, _ []string, registryName string) error {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return errors.Wrap(err, "failed getting config flag")
	}

	config, err := getConfig(registryName, configPath)
	if err != nil {
		return errors.Wrap(err, "get services config")
	}

	publishAuthOption := publish.WithAuthFromKeychain(authn.DefaultKeychain)
	remoteAuthOption := remote.WithAuthFromKeychain(authn.DefaultKeychain)
	maxGoRoutines, err := cmd.Flags().GetInt("max-go-routines")
	if err != nil {
		return errors.Wrap(err, "failed getting max-go-routines flag")
	}

	namespace, err := cmd.Flags().GetString("namespace")
	if err != nil {
		return errors.Wrap(err, "failed getting namespace flag")
	}

	imagesChan := make(chan *Image, len(config.ServicesConfig.GoServices))
	g := errgroup.Group{}
	g.SetLimit(maxGoRoutines)

	for i, service := range config.ServicesConfig.GoServices {
		// Build the first service separately to warm up the cache
		if i == 0 {
			// Assert that the first service is "inventory" (we want a big service to be first for the cache warmup)
			if service.Name != "inventory" {
				return errors.New("expected inventory service to be first")
			}
			log.Printf("ippon building first go service separately to warm up the cache: %+v\n", service)

			image, err := buildAndPublishService(ctx, service, config.ECR.URL(), namespace, publishAuthOption, remoteAuthOption)
			if err != nil {
				return errors.Wrap(err, "build and push go service")
			}
			imagesChan <- image
			continue
		}

		// Build remaining services in parallel
		service := service
		g.Go(func() error {
			log.Printf("ippon building go service: %+v\n", service)

			image, err := buildAndPublishService(ctx, service, config.ECR.URL(), namespace, publishAuthOption, remoteAuthOption)
			if err != nil {
				return errors.Wrap(err, "build and push go service")
			}
			imagesChan <- image
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errors.Wrap(err, "fatal error while building service")
	}
	close(imagesChan)

	if namespace == "" {
		return nil
	}
	return updateK8sDeployment(namespace, imagesChan)
}

// Helper function to extract common build and publish logic
func buildAndPublishService(ctx context.Context, service GoServiceConfig, baseURL, namespace string,
	publishAuthOption publish.Option, remoteAuthOption remote.Option) (*Image, error) {
	tags := service.GetTags()
	baseImage := service.GetBaseImage()

	// TODO: can probably separate build and publish in a different goroutine than build (io vs cpu)
	return buildAndPublishGoService(ctx, service.Main, service.Name, baseURL, baseImage,
		namespace, tags, publishAuthOption, remoteAuthOption)
}

func createMissingReposCommand(ctx context.Context, cmd *cobra.Command, _ []string, registryName string) error {
	configPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return errors.Wrap(err, "failed getting config flag")
	}
	config, err := getConfig(registryName, configPath)
	if err != nil {
		return errors.Wrap(err, "get services config")
	}

	namespace, err := cmd.Flags().GetString("namespace")
	if err != nil {
		return errors.Wrap(err, "failed getting namespace flag")
	}

	serviceNames := lo.Map(config.ServicesConfig.GoServices, func(s GoServiceConfig, _ int) string {
		return s.Name
	})

	for _, repo := range serviceNames {
		if namespace != "" {
			repo = path.Join(namespace, repo)
		}
		exists, err := config.ECR.RepositoryExists(ctx, repo)
		if err != nil {
			return err
		}

		if !exists {
			err := config.ECR.CreateRepository(ctx, repo)
			if err != nil {
				return err
			}
			log.Printf("repository created in registry: %s\n", repo)
		}
	}
	return nil
}
