package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"gopkg.in/yaml.v2"
)

// Config structure to parse environment files
type Config struct {
	Token     string            `yaml:"token"`
	Endpoints map[string]string `yaml:"endpoints"`
}

var execCmd = &cobra.Command{
	Use:   "exec [rpc] [service].[resource]",
	Short: "Execute a gRPC request to a specified service and message",
	Long: `Executes a gRPC command to a given service and message based on environment configuration.
  For example: cfctl exec list identity.User`,
	Args: cobra.ExactArgs(2),
	Run:  runExecCommand,
}

var parameters []string

func init() {
	rootCmd.AddCommand(execCmd)
	execCmd.Flags().StringArrayVarP(&parameters, "parameter", "p", []string{}, "Input Parameter (-p <key>=<value> -p ...)")
}

func loadConfig(environment string) (*Config, error) {
	configPath := fmt.Sprintf("%s/.spaceone/environments/%s.yml", os.Getenv("HOME"), environment)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("could not read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("could not unmarshal config: %w", err)
	}

	return &config, nil
}

func fetchCurrentEnvironment() (string, error) {
	envPath := fmt.Sprintf("%s/.spaceone/environment.yml", os.Getenv("HOME"))
	data, err := os.ReadFile(envPath)
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

func runExecCommand(cmd *cobra.Command, args []string) {
	environment, err := fetchCurrentEnvironment()
	if err != nil {
		log.Fatalf("Failed to get current environment: %v", err)
	}

	config, err := loadConfig(environment)
	if err != nil {
		log.Fatalf("Failed to load config for environment %s: %v", environment, err)
	}

	verbName := args[0]
	serviceResource := args[1]
	parts := strings.Split(serviceResource, ".")

	if len(parts) != 2 {
		log.Fatalf("Invalid service and resource format. Use [service].[resource]")
	}

	serviceName := parts[0]
	resourceName := parts[1]
	fullServiceName := fmt.Sprintf("spaceone.api.%s.v2.%s", serviceName, resourceName)

	endpoint, ok := config.Endpoints[serviceName]
	if !ok {
		log.Fatalf("Service endpoint not found for service: %s", serviceName)
	}

	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		log.Fatalf("Invalid endpoint URL %s: %v", endpoint, err)
	}

	grpcEndpoint := fmt.Sprintf("%s:%s", parsedURL.Hostname(), parsedURL.Port())

	// Set up secure connection
	conn, err := grpc.Dial(grpcEndpoint, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})))
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server: %v", err)
	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", config.Token)

	// Set up reflection client
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))

	// Get the service descriptor
	serviceDesc, err := refClient.ResolveService(fullServiceName)
	if err != nil {
		log.Fatalf("Failed to resolve service %s: %v", fullServiceName, err)
	}

	// Find the method descriptor
	methodDesc := serviceDesc.FindMethodByName(verbName)
	if methodDesc == nil {
		log.Fatalf("Method %s not found in service %s", verbName, fullServiceName)
	}

	// Create a dynamic message for the request
	inputType := methodDesc.GetInputType()
	reqMsg := dynamic.NewMessage(inputType)

	// Parse the input parameters into a map
	inputParams := parseParameters(parameters)
	for key, value := range inputParams {
		if err := reqMsg.TrySetFieldByName(key, value); err != nil {
			log.Fatalf("Failed to set field %s: %v", key, err)
		}
	}

	// Prepare response placeholder
	outputType := methodDesc.GetOutputType()
	respMsg := dynamic.NewMessage(outputType)

	// Make the RPC call using the client connection
	err = conn.Invoke(ctx, fmt.Sprintf("/%s/%s", serviceDesc.GetFullyQualifiedName(), methodDesc.GetName()), reqMsg, respMsg)
	if err != nil {
		log.Fatalf("Failed to call method %s: %v", verbName, err)
	}

	// Print the response
	fmt.Printf("Response: %+v\n", respMsg)
}

func parseParameters(params []string) map[string]string {
	parsed := make(map[string]string)
	for _, param := range params {
		parts := strings.SplitN(param, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("Invalid parameter format. Use key=value")
		}
		parsed[parts[0]] = parts[1]
	}
	return parsed
}
