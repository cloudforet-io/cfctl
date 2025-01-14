package format

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudforet-io/cfctl/pkg/configs"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ValidateServiceCommand checks if the given verb and resource are valid for the service
func ValidateServiceCommand(service, verb, resourceName string) error {
	// Get current environment from main setting file
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	mainV := viper.New()
	mainV.SetConfigFile(filepath.Join(home, ".cfctl", "setting.yaml"))
	mainV.SetConfigType("yaml")
	if err := mainV.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	currentEnv := mainV.GetString("environment")
	if currentEnv == "" {
		return fmt.Errorf("no environment set")
	}

	// Get environment config
	envConfig := mainV.Sub(fmt.Sprintf("environments.%s", currentEnv))
	if envConfig == nil {
		return fmt.Errorf("environment %s not found", currentEnv)
	}

	endpointName := envConfig.GetString("endpoint")
	if endpointName == "" {
		return fmt.Errorf("no endpoint found in configuration")
	}

	endpointName, _ = configs.GetAPIEndpoint(endpointName)

	// Fetch endpoints map
	endpointsMap, err := configs.FetchEndpointsMap(endpointName)
	if err != nil {
		return fmt.Errorf("failed to fetch endpoints: %v", err)
	}

	// Check if service exists
	serviceEndpoint, ok := endpointsMap[service]
	if !ok {
		return fmt.Errorf("service '%s' not found", service)
	}

	// Fetch service resources
	resources, err := FetchServiceResources(service, serviceEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch service resources: %v", err)
	}

	// Find the resource and check if the verb is valid
	resourceFound := false
	verbFound := false

	for _, row := range resources {
		if row[2] == resourceName {
			resourceFound = true
			verbs := strings.Split(row[1], ", ")
			for _, v := range verbs {
				if v == verb {
					verbFound = true
					break
				}
			}
			break
		}
	}

	if !resourceFound {
		return fmt.Errorf("resource '%s' not found in service '%s'", resourceName, service)
	}

	if !verbFound {
		return fmt.Errorf("verb '%s' not found for resource '%s' in service '%s'", verb, resourceName, service)
	}

	return nil
}

func FetchServiceResources(service, endpoint string, shortNamesMap map[string]string) ([][]string, error) {
	parts := strings.Split(endpoint, "://")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
	}

	scheme := parts[0]
	hostPort := strings.SplitN(parts[1], "/", 2)[0]

	var opts []grpc.DialOption
	if scheme == "grpc+ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", endpoint, err)
	}
	defer conn.Close()

	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create reflection client: %v", err)
	}

	req := &grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_ListServices{ListServices: ""},
	}

	if err := stream.Send(req); err != nil {
		return nil, fmt.Errorf("failed to send reflection request: %v", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("failed to receive reflection response: %v", err)
	}

	services := resp.GetListServicesResponse().Service

	// Load aliases
	aliases, err := configs.LoadAliases()
	if err != nil {
		return nil, fmt.Errorf("failed to load aliases: %v", err)
	}

	data := [][]string{}
	for _, s := range services {
		if strings.HasPrefix(s.Name, "grpc.reflection.v1alpha.") {
			continue
		}
		resourceName := s.Name[strings.LastIndex(s.Name, ".")+1:]
		verbs := getServiceMethods(client, s.Name)

		// Group verbs by alias
		verbsWithAlias := make(map[string]string)
		remainingVerbs := make([]string, 0)

		for _, verb := range verbs {
			hasAlias := false
			if serviceAliases, ok := aliases[service].(map[string]interface{}); ok {
				for alias, cmd := range serviceAliases {
					if cmdStr, ok := cmd.(string); ok {
						cmdParts := strings.Fields(cmdStr)
						if len(cmdParts) >= 2 &&
							cmdParts[0] == verb &&
							cmdParts[1] == resourceName {
							verbsWithAlias[verb] = alias
							hasAlias = true
							break
						}
					}
				}
			}
			if !hasAlias {
				remainingVerbs = append(remainingVerbs, verb)
			}
		}

		// Add row for verbs without aliases
		if len(remainingVerbs) > 0 {
			data = append(data, []string{service, strings.Join(remainingVerbs, ", "), resourceName, ""})
		}

		// Add separate rows for each verb with an alias
		for verb, alias := range verbsWithAlias {
			data = append(data, []string{service, verb, resourceName, alias})
		}
	}

	return data, nil
}

func getServiceMethods(client grpc_reflection_v1alpha.ServerReflectionClient, serviceName string) []string {
	stream, err := client.ServerReflectionInfo(context.Background())
	if err != nil {
		log.Fatalf("Failed to create reflection client: %v", err)
	}

	req := &grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: serviceName},
	}

	if err := stream.Send(req); err != nil {
		log.Fatalf("Failed to send reflection request: %v", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		log.Fatalf("Failed to receive reflection response: %v", err)
	}

	fileDescriptor := resp.GetFileDescriptorResponse()
	if fileDescriptor == nil {
		return []string{}
	}

	methods := []string{}
	for _, fdBytes := range fileDescriptor.FileDescriptorProto {
		fd := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(fdBytes, fd); err != nil {
			log.Fatalf("Failed to unmarshal file descriptor: %v", err)
		}
		for _, service := range fd.GetService() {
			if service.GetName() == serviceName[strings.LastIndex(serviceName, ".")+1:] {
				for _, method := range service.GetMethod() {
					methods = append(methods, method.GetName())
				}
			}
		}
	}

	return methods
}
