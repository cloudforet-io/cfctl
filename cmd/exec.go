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

	"github.com/golang/protobuf/ptypes/empty"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

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
		rpcVerb := args[0]

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
		resourceName := parts[1]

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

		// Receive and search for the specific service
		resp, err := stream.Recv()
		if err != nil {
			log.Fatalf("Failed to receive reflection response: %v", err)
		}

		serviceFound := false
		for _, svc := range resp.GetListServicesResponse().Service {
			if strings.Contains(svc.Name, resourceName) {
				serviceFound = true

				// Request file descriptor for the specific service
				fileDescriptorReq := &grpc_reflection_v1alpha.ServerReflectionRequest{
					MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_FileContainingSymbol{
						FileContainingSymbol: svc.Name,
					},
				}

				if err := stream.Send(fileDescriptorReq); err != nil {
					log.Fatalf("Failed to send file descriptor request: %v", err)
				}

				fileResp, err := stream.Recv()
				if err != nil {
					log.Fatalf("Failed to receive file descriptor response: %v", err)
				}

				// Parse the file descriptor response
				fd := fileResp.GetFileDescriptorResponse()
				if fd == nil {
					log.Fatalf("No file descriptor found for service %s", svc.Name)
				}

				// Extract methods from the file descriptor
				fmt.Printf("Available methods for service %s:\n", svc.Name)
				methodFound := false
				for _, b := range fd.FileDescriptorProto {
					protoDescriptor := &descriptorpb.FileDescriptorProto{}
					if err := proto.Unmarshal(b, protoDescriptor); err != nil {
						log.Fatalf("Failed to unmarshal file descriptor proto: %v", err)
					}

					for _, service := range protoDescriptor.Service {
						if service.GetName() == resourceName {
							for _, method := range service.Method {
								fmt.Printf("- %s\n", method.GetName())
								if method.GetName() == rpcVerb {
									methodFound = true
									// Call the method if it matches
									fmt.Printf("Calling method %s on service %s...\n", rpcVerb, svc.Name)

									// Assuming the list method has no parameters
									// Prepare the request message (in this case, an empty request)
									req := &empty.Empty{}
									response := new(empty.Empty) // Create a response placeholder

									// Make the RPC call using the client connection
									err = conn.Invoke(ctx, fmt.Sprintf("/%s/%s", svc.Name, method.GetName()), req, response)
									if err != nil {
										log.Fatalf("Failed to call method %s: %v", rpcVerb, err)
									}

									// Print the response
									fmt.Printf("Response: %+v\n", response)
								}
							}
						}
					}
				}

				if !methodFound {
					log.Fatalf("Method %s not found in service %s", rpcVerb, resourceName)
				}

				break
			}
		}

		if !serviceFound {
			log.Fatalf("Service %s not found", resourceName)
		}
	},
}

func init() {
	rootCmd.AddCommand(execCmd)
}
