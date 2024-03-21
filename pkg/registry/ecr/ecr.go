package ecr

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/pkg/errors"
)

type ECR struct {
	accountId string
	region    string
	client    *ecr.Client
}

func NewECR(accountId, region string) *ECR {
	return &ECR{
		accountId: accountId,
		region:    region,
	}
}

func (this *ECR) Init(ctx context.Context) error {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(this.region))
	if err != nil {
		return err
	}
	this.client = ecr.NewFromConfig(cfg)
	return nil
}

func (this *ECR) URL() string {
	return fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", this.accountId, this.region)
}

func (this *ECR) GetRepositoryURL(name string) string {
	return fmt.Sprintf("%s/%s", this.URL(), name)
}

func (this *ECR) RepositoryExists(ctx context.Context, repo string) (bool, error) {
	if this.client == nil {
		return false, errors.New("ECR is not initialized")
	}

	params := &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{repo},
	}

	_, err := this.client.DescribeRepositories(ctx, params)
	if err != nil {
		if strings.Contains(err.Error(), "RepositoryNotFoundException") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (this *ECR) CreateRepository(ctx context.Context, repo string) error {
	if this.client == nil {
		return errors.New("ECR is not initialized")
	}

	return this.createRepo(ctx, repo)
}

func (this *ECR) createRepo(ctx context.Context, repo string) error {
	params := &ecr.CreateRepositoryInput{
		RepositoryName: &repo,
	}

	_, err := this.client.CreateRepository(ctx, params)
	return err
}
