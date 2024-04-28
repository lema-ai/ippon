package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/google/ko/pkg/publish"
	yqcmd "github.com/mikefarah/yq/v4/cmd"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	GetAuthOption() publish.Option
}

const (
	defaultBaseImage = "cgr.dev/chainguard/busybox:latest"
	configFileName   = "ippon"
	configEnvPrefix  = "IPPON"
)

var (
	outputBuffer bytes.Buffer // easier debugging in case of errors, buffer to store output when running in non verbose mode
)

func tryCallParentPersistentPreRun(cmd *cobra.Command, args []string) error {
	if parent := cmd.Parent(); parent != nil {
		if parent.PersistentPreRunE != nil {
			return parent.PersistentPreRunE(parent, args)
		}
	}
	return nil
}

func buildRegistryCommand(cmdName string) (*cobra.Command, error) {
	ctx := context.Background()
	registryCmd := &cobra.Command{
		Use:  cmdName,
		Args: cobra.MinimumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return tryCallParentPersistentPreRun(cmd, args)
		},
	}

	releaseCmd := &cobra.Command{
		Use:   "release",
		Short: "Build, tag and push an image",
		RunE: func(cmd *cobra.Command, args []string) error {
			return registryCommand(ctx, cmd, args, cmdName)
		},
	}
	releaseCmd.Flags().Int("max-go-routines", 5, "Maximum number of go routines to use for building and pushing images concurrently. Default is 5.")
	releaseCmd.Flags().String("namespace", "", "Okteto namespace to update the kustomization file with the new image digests")
	releaseCmd.Flags().String("config", "ippon.yaml", "Path to ippon config file")
	registryCmd.AddCommand(releaseCmd)

	createMissingCmd := &cobra.Command{
		Use:   "create-missing-repos",
		Short: "Create required and missing repositories in the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return createMissingReposCommand(ctx, cmd, args, cmdName)
		},
	}
	createMissingCmd.Flags().String("namespace", "", "Okteto namespace to use for the missing repositories")
	createMissingCmd.Flags().String("config", "ippon.yaml", "Path to ippon config file")
	registryCmd.AddCommand(createMissingCmd)

	return registryCmd, nil
}

func finishWithError(msg string, err error) {
	fmt.Print(outputBuffer.String())
	log.SetOutput(os.Stdout)
	log.Fatalf("%s: %v\n", msg, err)
}

func init() {
	viper.SetConfigName(configFileName)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.SetDefault("base_image", defaultBaseImage)
	viper.SetEnvPrefix(configEnvPrefix)
	viper.AutomaticEnv()
}

func main() {
	oktetoCommand, err := buildRegistryCommand("okteto")
	if err != nil {
		finishWithError("failed creating okteto command", err)
	}

	releaseCommand, err := buildRegistryCommand("release")
	if err != nil {
		finishWithError("failed creating release command", err)
	}

	// so we don't require everyone to install yq directly
	// thankfully it's written in Go and with cobra!
	yqCmd := yqcmd.New()

	rootCmd := &cobra.Command{
		Use:   "ippon",
		Short: "Ippon build and release Go images",
		Long:  "Ippon make it easy to handle Go images release in a micro-services architecture",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			verbose, err := cmd.Flags().GetBool("verbose")
			if err != nil {
				return errors.Wrap(err, "failed getting verbose flag")
			}
			if verbose {
				log.SetOutput(os.Stdout)
			} else {
				log.SetOutput(io.Discard)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().Bool("verbose", false, "verbose output")
	rootCmd.AddCommand(oktetoCommand, releaseCommand, yqCmd)
	err = rootCmd.Execute()
	if err != nil {
		finishWithError("failed executing command", err)
	}
}
