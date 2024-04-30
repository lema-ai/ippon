package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/ko/pkg/build"
	"github.com/google/ko/pkg/publish"
	"github.com/lema-ai/ippon/registry"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

func authDockerEcr(accountId, region string) error {
	awsAuthArgs := []string{"ecr", "get-login-password", "--region", region}
	dockerLoginArgs := []string{"login", "--username", "AWS", "--password-stdin", fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountId, region)}
	awsAuthCmd := exec.Command("aws", awsAuthArgs...)
	dockerLoginCmd := exec.Command("docker", dockerLoginArgs...)

	var err error
	dockerLoginCmd.Stdin, err = awsAuthCmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "Failed getting aws auth command stdout")
	}

	dockerLoginCmd.Stdout = os.Stdout
	dockerLoginCmd.Stderr = os.Stderr
	err = dockerLoginCmd.Start()
	if err != nil {
		return errors.Wrap(err, "Failed starting docker login command")
	}

	err = awsAuthCmd.Run()
	if err != nil {
		return errors.Wrap(err, "Failed running aws auth command")
	}

	return dockerLoginCmd.Wait()
}

func buildDockerImage(repoURL, repoName, dockerfilePath, target, cacheToTarget string, cacheFromTargets, tags []string) error {
	buildArgs := []string{"buildx", "build", "--output", "type=registry", "--platform=linux/amd64", "--progress=plain", "--push", "--build-arg", "BUILDKIT_INLINE_CACHE=1",
		"--cache-to", fmt.Sprintf("mode=max,image-manifest=true,oci-mediatypes=true,type=registry,ref=%s/cache/%s:cache", repoURL, cacheToTarget)}
	for _, cacheFromTarget := range cacheFromTargets {
		buildArgs = append(buildArgs, "--cache-from", fmt.Sprintf("mode=max,image-manifest=true,oci-mediatypes=true,type=registry,ref=%s/cache/%s:cache", repoURL, cacheFromTarget))
	}
	if target != "" {
		buildArgs = append(buildArgs, "--target", target)
	}
	for _, tag := range tags {
		buildArgs = append(buildArgs, "-t", fmt.Sprintf("%s:%s", repoName, tag))
	}
	buildArgs = append(buildArgs, "-f", dockerfilePath, ".")

	buildCmd := exec.Command("docker", buildArgs...)
	buildCmd.Env = append(os.Environ(), "DOCKER_BUILDKIT=1")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	return buildCmd.Run()
}

type dockerManifest struct {
	Descriptor struct {
		Digest string `json:"digest"`
	} `json:"Descriptor"`
}

func getDockerImageDigest(repoName, tag string) (string, error) {
	digestArgs := []string{"manifest", "inspect", "--verbose", fmt.Sprintf("%s:%s", repoName, tag)}
	digestCmd := exec.Command("docker", digestArgs...)
	digestCmd.Stderr = os.Stderr

	digestBytes, err := digestCmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "get docker image digest")
	}

	var manifests []dockerManifest
	err = json.Unmarshal(digestBytes, &manifests)
	if err != nil || len(manifests) == 0 {
		fmt.Println(string(digestBytes))
		return "", errors.Wrap(err, "unmarshal docker manifest")
	}

	return manifests[0].Descriptor.Digest, nil
}

func buildAndPublishDockerService(ecr *registry.ECR, serviceName, dockerfilePath, target, namespace string, allTargets, tags []string) (*Image, error) {
	repoURL := ecr.URL()
	repoName := ecr.GetRepositoryURL(fmt.Sprintf("%s/%s", namespace, serviceName))
	cacheTargets := lo.Map(allTargets, func(t string, _ int) string {
		return t
	})

	err := buildDockerImage(repoURL, repoName, dockerfilePath, target, serviceName, cacheTargets, tags)
	if err != nil {
		return nil, errors.Wrap(err, "build docker image")
	}

	digest, err := getDockerImageDigest(repoName, tags[0])
	if err != nil {
		return nil, errors.Wrap(err, "get docker image digest")
	}

	return &Image{
		OldName: fmt.Sprintf("registry.lema.ai/%s", serviceName),
		NewName: fmt.Sprintf("%s@%s", repoName, digest),
	}, nil
}

func buildAndPublishGoService(ctx context.Context, cmdDir, serviceName, baseURL, baseImage, namespace string, tags []string, authOption publish.Option) (*Image, error) {
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

	authOption := publish.WithAuthFromKeychain(authn.DefaultKeychain)
	maxGoRoutines, err := cmd.Flags().GetInt("max-go-routines")
	if err != nil {
		return errors.Wrap(err, "failed getting max-go-routines flag")
	}

	namespace, err := cmd.Flags().GetString("namespace")
	if err != nil {
		return errors.Wrap(err, "failed getting namespace flag")
	}

	lenDockerServices := lo.Sum(lo.Map(config.ServicesConfig.DockerServices, func(s DockerServiceConfig, _ int) int {
		return len(s.TargetsOrder)
	}))
	imagesChan := make(chan *Image, len(config.ServicesConfig.GoServices)+lenDockerServices)
	g := errgroup.Group{}
	g.SetLimit(maxGoRoutines)

	err = authDockerEcr(config.ECR.AccountId(), config.ECR.Region())
	if err != nil {
		return errors.Wrap(err, "auth docker ecr")
	}

	for _, service := range config.ServicesConfig.DockerServices {
		service := service

		log.Printf("ippon building docker service: %+v\n", service)
		tags := service.GetTags()
		targets := lo.Map(service.TargetsOrder, func(t Target, _ int) string {
			return t.Name
		})
		g.Go(func() error {
			for _, target := range service.TargetsOrder {
				target := target
				image, err := buildAndPublishDockerService(config.ECR, target.Name, service.Dockerfile, target.Target, namespace, targets, tags)
				if err != nil {
					return errors.Wrap(err, "build and push docker service")
				}

				imagesChan <- image
			}
			return nil
		})
	}

	for _, service := range config.ServicesConfig.GoServices {
		service := service
		g.Go(func() error {
			log.Printf("ippon building go service: %+v\n", service)
			baseURL := config.ECR.URL()
			tags := service.GetTags()
			baseImage := service.GetBaseImage()

			image, err := buildAndPublishGoService(ctx, service.Main, service.Name, baseURL, baseImage, namespace, tags, authOption)
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
	if namespace == "" {
		return errors.New("empty namespace flag is not supported for create-missing-repos command")
	}

	serviceNames := lo.Map(config.ServicesConfig.GoServices, func(s GoServiceConfig, _ int) string {
		return s.Name
	})
	for _, service := range config.ServicesConfig.DockerServices {
		serviceNames = append(serviceNames, lo.Map(service.TargetsOrder, func(t Target, _ int) string {
			return t.Name
		})...)
	}

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
