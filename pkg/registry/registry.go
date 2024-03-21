package registry

import (
	"context"
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
	Username() string
	Password() string
}
