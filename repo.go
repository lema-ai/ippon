package main

import (
	"context"
	"log"
	"path"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/ko/pkg/build"
	"github.com/google/ko/pkg/publish"
	"github.com/lema-ai/ippon/registry"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func buildAndPublishService(ctx context.Context, cmdDir, serviceName, baseURL, baseImage, namespace string, tags []string, authOption publish.Option) (*k8sRenameInfo, error) {
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
		authOption,
	)
	if err != nil {
		return nil, errors.Wrap(err, "authenticate to image repo")
	}

	repoName := serviceName
	if namespace != "" {
		repoName = path.Join(namespace, serviceName)
	}

	ref, err := p.Publish(ctx, r, repoName)
	if err != nil {
		return nil, errors.Wrap(err, "publish image")
	}

	return &k8sRenameInfo{
		name:   serviceName,
		image:  ref.Context().Name(),
		digest: digest.String(),
	}, nil
}

func registryCommand(ctx context.Context, cmd *cobra.Command, _ []string, servicesConfig *ServicesConfig, registry *registry.ECR) error {
	authOption := publish.WithAuthFromKeychain(authn.DefaultKeychain)
	maxGoRoutines, err := cmd.Flags().GetInt("max-go-routines")
	if err != nil {
		return errors.Wrap(err, "failed getting max-go-routines flag")
	}

	namespace, err := cmd.Flags().GetString("namespace")
	if err != nil {
		return errors.Wrap(err, "failed getting namespace flag")
	}

	k8sInfoChan := make(chan *k8sRenameInfo, len(servicesConfig.Services))
	g := errgroup.Group{}
	g.SetLimit(maxGoRoutines)
	for _, service := range servicesConfig.Services {
		service := service
		g.Go(func() error {
			log.Printf("ippon building service: %+v\n", service)
			baseURL := registry.URL()
			tags := service.GetTags()
			baseImage := service.GetBaseImage()

			info, err := buildAndPublishService(ctx, service.Main, service.Name, baseURL, baseImage, namespace, tags, authOption)
			if err != nil {
				return errors.Wrap(err, "build and push service")
			}

			k8sInfoChan <- info
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return errors.Wrap(err, "fatal error while building service")
	}

	close(k8sInfoChan)

	if namespace == "" {
		return nil
	}

	return updateK8sDeployment(k8sInfoChan)
}

func createMissingReposCommand(ctx context.Context, cmd *cobra.Command, _ []string, servicesConfig *ServicesConfig, registry *registry.ECR) error {
	{
		namespace, err := cmd.Flags().GetString("namespace")
		if err != nil {
			return errors.Wrap(err, "failed getting namespace flag")
		}
		if namespace == "" {
			return errors.New("empty namespace flag is not supported for create-missing-repos command")
		}

		for _, s := range servicesConfig.Services {
			repo := s.Name
			if namespace != "" {
				repo = path.Join(namespace, repo)
			}
			exists, err := registry.RepositoryExists(ctx, repo)
			if err != nil {
				return err
			}

			if !exists {
				err := registry.CreateRepository(ctx, repo)
				if err != nil {
					return err
				}
				log.Printf("repository created in registry: %s\n", repo)
			}
		}
		return nil
	}
}
