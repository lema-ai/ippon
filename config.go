package main

import (
	"context"
	"os"

	"github.com/lema-ai/ippon/registry"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

type Config struct {
	ECR            *registry.ECR
	ServicesConfig *ServicesConfig
}

type ServicesConfig struct {
	GoServices []GoServiceConfig `mapstructure:"go_services"`
}

type Target struct {
	Name   string `mapstructure:"name"`
	Target string `mapstructure:"target"`
}

type GoServiceConfig struct {
	Name      string   `mapstructure:"name"`
	Tags      []string `mapstructure:"tags"`
	Main      string   `mapstructure:"main"`
	BaseImage string   `mapstructure:"base_image"`
}

type ExcludedServices struct {
	Services []string `yaml:"services"`
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

func readExcludedServices(excludeServicesPath string) ([]string, bool, error) {
	// Check if the file exists
	if _, err := os.Stat(excludeServicesPath); os.IsNotExist(err) {
		return []string{}, false, nil // Return empty slice and false if file doesn't exist
	}
	
	// Read the file
	data, err := os.ReadFile(excludeServicesPath)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed reading excluded services file")
	}
	
	// Parse the YAML
	var excluded ExcludedServices
	err = yaml.Unmarshal(data, &excluded)
	if err != nil {
		return nil, false, errors.Wrap(err, "failed parsing excluded services file")
	}
	
	return excluded.Services, true, nil
}

func filterServices(services []GoServiceConfig, excludedServices []string) []GoServiceConfig {
	if len(excludedServices) == 0 {
		return services
	}
	
	// Create a map for faster lookup
	excludedMap := make(map[string]bool)
	for _, service := range excludedServices {
		excludedMap[service] = true
	}
	
	// Filter services
	var filteredServices []GoServiceConfig
	for _, service := range services {
		if !excludedMap[service.Name] {
			filteredServices = append(filteredServices, service)
		}
	}
	
	return filteredServices
}
