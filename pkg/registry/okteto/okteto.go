package okteto

import (
	"context"
	"fmt"
	"os"

	"github.com/pkg/errors"
)

type Registry struct {
	registryUrl string
	namespace   string
	username    string
	token       string
}

func (this *Registry) Init(ctx context.Context) error {
	registryUrl, exists := os.LookupEnv("OKTETO_REGISTRY_URL")
	if !exists {
		return errors.New("Failed getting Okteto's registry: OKTETO_REGISTRY_URL not set")
	}
	this.registryUrl = registryUrl

	namespace, exists := os.LookupEnv("OKTETO_NAMESPACE")
	if !exists {
		return errors.New("Failed getting Okteto's registry: OKTETO_NAMESPACE not set")
	}
	this.namespace = namespace

	username, exists := os.LookupEnv("OKTETO_USERNAME")
	if !exists {
		return errors.New("Failed getting Okteto's registry: OKTETO_USERNAME not set")
	}
	this.username = username

	token, exists := os.LookupEnv("OKTETO_TOKEN")
	if !exists {
		return errors.New("Failed getting Okteto's registry: OKTETO_TOKEN not set")
	}
	this.token = token

	return nil
}

func (this *Registry) Username() string {
	return this.username
}

func (this *Registry) Password() string {
	return this.token
}

//	func (this *Registry) GetAuthOption() publish.Option {
//		return publish.WithAuth(&authn.Basic{
//			Username: this.username,
//			Password: this.token,
//		})
//	}
func (this *Registry) URL() string {
	return fmt.Sprintf("%s/%s", this.registryUrl, this.namespace)
}
