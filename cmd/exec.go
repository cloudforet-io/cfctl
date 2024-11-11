/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"gopkg.in/yaml.v2"
)

// Config structure to parse environment files
type Config struct {
	Token     string            `yaml:"token"`
	Endpoints map[string]string `yaml:"endpoints"`
}

// Load environment configuration
func loadConfig(environment string) (*Config, error) {
	configPath := fmt.Sprintf("%s/.spaceone/environments/%s.yml", os.Getenv("HOME"), environment)
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("could not read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("could not unmarshal config: %w", err)
	}

	return &config, nil
}

// Load current environment from environment.yml
func fetchCurrentEnvironment() (string, error) {
	envPath := fmt.Sprintf("%s/.spaceone/environment.yml", os.Getenv("HOME"))

	data, err := ioutil.ReadFile(envPath)
	if err != nil {
		return "", fmt.Errorf("could not read environment file: %w", err)
	}

	var envConfig struct {
		Environment string `yaml:"environment"`
	}

	if err := yaml.Unmarshal(data, &envConfig); err != nil {
		return "", fmt.Errorf("could not unmarshal environment config: %w", err)
	}

	return envConfig.Environment, nil
}

// execCmd represents the exec command
var execCmd = &cobra.Command{
	Use:   "exec [verb] [service].[resource]",
	Short: "Execute a gRPC request to a specified service and resource",
	Long: `Executes a gRPC command to a given service and resource based on environment configuration.
	For example: cfctl exec list identity.User`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		// Verb and service resource extraction
		serviceResource := args[1]

		// Load environment
		environment, err := fetchCurrentEnvironment()
		if err != nil {
			log.Fatalf("Failed to get current environment: %v", err)
		}

		config, err := loadConfig(environment)
		if err != nil {
			log.Fatalf("Failed to load config for environment %s: %v", environment, err)
		}

		// Extract service name
		parts := strings.Split(serviceResource, ".")
		if len(parts) != 2 {
			log.Fatalf("Invalid service format. Use [service].[resource]")
		}
		serviceName := parts[0]

		// Modify endpoint format
		endpoint := config.Endpoints[serviceName]
		endpoint = strings.Replace(strings.Replace(endpoint, "grpc+ssl://", "", 1), "/v1", "", 1)

		// Set up secure connection
		conn, err := grpc.Dial(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
		if err != nil {
			log.Fatalf("Failed to connect to gRPC server: %v", err)
		}
		defer conn.Close()

		// Set up gRPC reflection client
		refClient := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
		ctx := metadata.AppendToOutgoingContext(context.Background(), "token", config.Token)

		stream, err := refClient.ServerReflectionInfo(ctx)
		if err != nil {
			log.Fatalf("Failed to create reflection stream: %v", err)
		}

		// Request service list
		req := &grpc_reflection_v1alpha.ServerReflectionRequest{
			MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{
				ListServices: "*",
			},
		}

		if err := stream.Send(req); err != nil {
			log.Fatalf("Failed to send reflection request: %v", err)
		}

		// Receive and print reflection response
		resp, err := stream.Recv()
		if err != nil {
			log.Fatalf("Failed to receive reflection response: %v", err)
		}
		fmt.Println(resp.MessageResponse)
	},
}

func init() {
	rootCmd.AddCommand(execCmd)
}
