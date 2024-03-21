package build

import (
	"context"

	"github.com/lema-ai/ippon/pkg/registry"
)

type BuildOptions struct {
	Platform []string
	// WithSBOM bool
}

type Builder interface {
	Build(ctx context.Context, options BuildOptions) (Publisher, error)
}

type PublishOptions struct {
	ImageName string
	Tags      []string
}

type Publisher interface {
	// Publish(builder Builder) error
	Publish(ctx context.Context, reg registry.Registry, opts PublishOptions) error
}

func BuildAndPublish(ctx context.Context, builder Builder, buildOpts BuildOptions, registry registry.Registry, publishOpts PublishOptions) error {
	publisher, err := builder.Build(ctx, buildOpts)
	if err != nil {
		return err
	}

	return publisher.Publish(ctx, registry, publishOpts)
}
