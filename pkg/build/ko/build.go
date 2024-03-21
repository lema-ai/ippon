package ko

import (
	"context"
	"log"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ko_build "github.com/google/ko/pkg/build"
	"github.com/google/ko/pkg/publish"
	"github.com/lema-ai/ippon/pkg/build"
	"github.com/lema-ai/ippon/pkg/registry"
	"github.com/pkg/errors"
)

const defaultBaseImage = "cgr.dev/chainguard/busybox:latest"

type Builder struct {
	cmdDir    string
	baseImage string
}

func NewBuilder(cmdDir, baseImage string) *Builder {
	return &Builder{
		cmdDir:    cmdDir,
		baseImage: baseImage,
	}
}

func (this *Builder) Build(ctx context.Context, opts build.BuildOptions) (build.Publisher, error) {
	b, err := ko_build.NewGo(ctx, this.cmdDir,
		ko_build.WithPlatforms(opts.Platform...),
		ko_build.WithDisabledSBOM(),
		ko_build.WithBaseImages(func(ctx context.Context, _ string) (name.Reference, ko_build.Result, error) {
			ref, err := name.ParseReference(this.baseImage)
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

	return newPublisher(r), nil
}

type publisher struct {
	buildResult ko_build.Result
}

func newPublisher(buildResult ko_build.Result) *publisher {
	return &publisher{
		buildResult: buildResult,
	}
}

func (this *publisher) Publish(ctx context.Context, reg registry.Registry, opts build.PublishOptions) error {
	tags := opts.Tags
	digest, err := this.buildResult.Digest()
	if err != nil {
		return errors.Wrap(err, "get image digest")
	}

	digestTag := strings.TrimPrefix(digest.String(), "sha256:")
	tags = append(tags, digestTag)

	authOption := getRegistryAuthOption(reg)

	p, err := publish.NewDefault(reg.URL(),
		publish.WithTags(tags),
		authOption,
	)
	if err != nil {
		return errors.Wrap(err, "authenticate to image repo")
	}

	ref, err := p.Publish(ctx, this.buildResult, opts.ImageName)
	if err != nil {
		return errors.Wrap(err, "publish image")
	}

	log.Println(ref.String())
	return nil
}

func getRegistryAuthOption(reg registry.Registry) publish.Option {
	if authReg, ok := reg.(registry.SelfAuthRegistry); ok {
		return publish.WithAuth(&authn.Basic{
			Username: authReg.Username(),
			Password: authReg.Password(),
		})
	}

	// use credentials from ~/.docker/config.json.
	log.Println("Using the default docker config.json credentials for login")
	return publish.WithAuthFromKeychain(authn.DefaultKeychain)
}
