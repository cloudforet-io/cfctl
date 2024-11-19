// common/methods.go

package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/jhump/protoreflect/grpcreflect"
)

// AddVerbCommands adds subcommands for each Verb to the parent command
func AddVerbCommands(parentCmd *cobra.Command, serviceName string, groupID string) error {
	verbs, err := GetUniqueVerbsForService(serviceName)
	if err != nil {
		return fmt.Errorf("failed to get verbs for service %s: %v", serviceName, err)
	}

	for _, verb := range verbs {
		currentVerb := verb
		verbCmd := &cobra.Command{
			Use:   currentVerb + " <resource>",
			Short: fmt.Sprintf("%s %s command", currentVerb, serviceName),
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				resource := args[0]
				return ExecuteCommand(serviceName, currentVerb, resource)
			},
			GroupID: groupID,
		}
		parentCmd.AddCommand(verbCmd)
	}

	return nil
}

// GetUniqueVerbsForService fetches all unique Verbs (methods) for a given service.
func GetUniqueVerbsForService(serviceName string) ([]string, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	var envPrefix string
	if strings.HasPrefix(config.Environment, "dev-") {
		envPrefix = "dev"
	} else if strings.HasPrefix(config.Environment, "stg-") {
		envPrefix = "stg"
	} else {
		return nil, fmt.Errorf("unsupported environment prefix")
	}
	hostPort := fmt.Sprintf("%s.api.%s.spaceone.dev:443", serviceName, envPrefix)

	// Configure gRPC connection
	var opts []grpc.DialOption
	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}
	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.WithTransportCredentials(creds))

	// Establish the connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", hostPort, err)
	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", config.Environments[config.Environment].Token)
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// List all services
	services, err := refClient.ListServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %v", err)
	}

	verbsSet := make(map[string]struct{})

	for _, s := range services {
		if strings.HasPrefix(s, "grpc.reflection.") {
			continue
		}

		if !strings.Contains(s, fmt.Sprintf(".%s.", serviceName)) {
			continue
		}

		serviceDesc, err := refClient.ResolveService(s)
		if err != nil {
			continue
		}

		for _, method := range serviceDesc.GetMethods() {
			verbsSet[method.GetName()] = struct{}{}
		}
	}

	// Convert the set to a slice
	var uniqueVerbs []string
	for verb := range verbsSet {
		uniqueVerbs = append(uniqueVerbs, verb)
	}

	// Sort the verbs
	sort.Strings(uniqueVerbs)

	return uniqueVerbs, nil
}

func ExecuteCommand(serviceName, verb, resource string) error {
	// Implement the logic to execute the command
	// For example, make a gRPC call to the service with the specified verb and resource

	fmt.Printf("Executing %s %s %s\n", serviceName, verb, resource)

	// TODO: Add the actual execution logic here

	return nil
}
