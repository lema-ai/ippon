package main

import (
	"context"
	"os"

	"github.com/lema-ai/ippon/registry"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

type Config struct {
	ECR            *registry.ECR
	ServicesConfig *ServicesConfig
}

type ServicesConfig struct {
	GoServices     []GoServiceConfig     `mapstructure:"go_services"`
	DockerServices []DockerServiceConfig `mapstructure:"docker_services"`
}

type DockerServiceConfig struct {
	Tags       []string `mapstructure:"tags"`
	Dockerfile string   `mapstructure:"dockerfile"`
	// For multi-target build files.
	// If nothing is passed here, will build a single target with the configured Name.
	TargetsOrder []Target `mapstructure:"targets_order"`
}

type Target struct {
	Name   string `mapstructure:"name"`
	Target string `mapstructure:"target"`
}

func (this DockerServiceConfig) GetTags() []string {
	if this.Tags != nil {
		return this.Tags
	}

	return viper.GetStringSlice("tags")
}

type GoServiceConfig struct {
	Name      string   `mapstructure:"name"`
	Tags      []string `mapstructure:"tags"`
	Main      string   `mapstructure:"main"`
	BaseImage string   `mapstructure:"base_image"`
}

func (this GoServiceConfig) GetTags() []string {
	if this.Tags != nil {
		return this.Tags
	}

	return viper.GetStringSlice("tags")
}

func (this GoServiceConfig) GetBaseImage() string {
	if this.BaseImage != "" {
		return this.BaseImage
	}

	return viper.GetString("base_image")
}

func getConfig(registryName, path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed opening config file")
	}
	defer f.Close()

	err = viper.ReadConfig(f)
	if err != nil {
		return nil, errors.Wrap(err, "failed reading config file")
	}

	var services ServicesConfig
	err = viper.Unmarshal(&services)
	if err != nil {
		return nil, errors.Wrap(err, "failed unmarshalling config file")
	}

	accountID := viper.GetString(registryName + ".account")
	region := viper.GetString(registryName + ".region")
	ctx := context.Background()
	ecr, err := registry.NewECR(ctx, accountID, region)
	if err != nil {
		return nil, errors.Wrap(err, "failed creating ECR client")
	}

	config := &Config{
		ECR:            ecr,
		ServicesConfig: &services,
	}

	return config, nil
}
