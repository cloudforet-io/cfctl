package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudforet-io/cfctl/pkg/configs"
	"github.com/cloudforet-io/cfctl/pkg/format"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"gopkg.in/yaml.v3"
)

// FetchApiResourcesCmd provides api-resources command for the given service
func FetchApiResourcesCmd(serviceName string) *cobra.Command {
	return &cobra.Command{
		Use:   "api_resources",
		Short: fmt.Sprintf("Displays supported API resources for the %s service", serviceName),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ListAPIResources(serviceName)
		},
	}
}

func ListAPIResources(serviceName string) error {
	setting, err := configs.SetSettingFile()
	if err != nil {
		return fmt.Errorf("failed to load setting: %v", err)
	}

	//endpoint, err := getServiceEndpoint(setting, serviceName)
	endpoint, err := configs.GetServiceEndpoint(setting, serviceName)
	if err != nil {
		return fmt.Errorf("failed to get endpoint for service %s: %v", serviceName, err)
	}

	shortNamesMap, err := loadShortNames()
	if err != nil {
		return fmt.Errorf("failed to load short names: %v", err)
	}

	data, err := FetchServiceResources(serviceName, endpoint, shortNamesMap, setting)
	if err != nil {
		return fmt.Errorf("failed to fetch resources for service %s: %v", serviceName, err)
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	format.RenderTable(data)

	return nil
}

func loadShortNames() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}
	shortNamesFile := filepath.Join(home, ".cfctl", "short_names.yaml")
	shortNamesMap := make(map[string]string)
	if _, err := os.Stat(shortNamesFile); err == nil {
		file, err := os.Open(shortNamesFile)
		if err != nil {
			return nil, fmt.Errorf("failed to open short_names.yaml file: %v", err)
		}
		defer file.Close()

		err = yaml.NewDecoder(file).Decode(&shortNamesMap)
		if err != nil {
			return nil, fmt.Errorf("failed to decode short_names.yaml: %v", err)
		}
	}
	return shortNamesMap, nil
}

func FetchServiceResources(serviceName, endpoint string, shortNamesMap map[string]string, config *configs.Environments) ([][]string, error) {
	parts := strings.Split(endpoint, "://")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
	}
	scheme := parts[0]
	hostPort := parts[1]

	var opts []grpc.DialOption
	if scheme == "grpc+ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else if scheme == "grpc" {
		if strings.Contains(hostPort, ".svc.cluster.local") {
			tlsConfig := &tls.Config{
				InsecureSkipVerify: true,
			}
			creds := credentials.NewTLS(tlsConfig)
			opts = append(opts, grpc.WithTransportCredentials(creds))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		}
	} else {
		return nil, fmt.Errorf("unsupported scheme: %s", scheme)
	}

	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", endpoint, err)
	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", config.Environments[config.Environment].Token)

	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	services, err := refClient.ListServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %v", err)
	}

	// Load short names from setting.yaml
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	serviceShortNames := make(map[string]string)
	if err := v.ReadInConfig(); err == nil {
		// Get short names for this service
		shortNamesSection := v.GetStringMap(fmt.Sprintf("short_names.%s", serviceName))
		for shortName, cmd := range shortNamesSection {
			if cmdStr, ok := cmd.(string); ok {
				serviceShortNames[shortName] = cmdStr
			}
		}
	}

	data := [][]string{}
	resourceData := make(map[string][][]string)

	for _, s := range services {
		if strings.HasPrefix(s, "grpc.reflection.") {
			continue
		}

		displayServiceName := serviceName
		if strings.HasPrefix(endpoint, "grpc://") && (strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1")) {
			parts := strings.Split(s, ".")
			if len(parts) > 2 {
				serviceDesc, err := refClient.ResolveService(s)
				if err != nil {
					log.Printf("Failed to resolve service %s: %v", s, err)
					continue
				}

				resourceName := s[strings.LastIndex(s, ".")+1:]
				verbs := []string{}
				for _, method := range serviceDesc.GetMethods() {
					verbs = append(verbs, method.GetName())
				}

				sort.Strings(verbs)
				data = append(data, []string{
					displayServiceName,
					strings.Join(verbs, ", "),
					resourceName,
					"",
				})
				continue
			}
		} else if !strings.Contains(s, fmt.Sprintf(".%s.", serviceName)) {
			continue
		}

		serviceDesc, err := refClient.ResolveService(s)
		if err != nil {
			log.Printf("Failed to resolve service %s: %v", s, err)
			continue
		}

		resourceName := s[strings.LastIndex(s, ".")+1:]
		verbs := []string{}
		for _, method := range serviceDesc.GetMethods() {
			verbs = append(verbs, method.GetName())
		}

		// Create a map to track which verbs have been used in short names
		usedVerbs := make(map[string]bool)
		resourceRows := [][]string{}

		// First, check for verbs with short names
		for shortName, cmdStr := range serviceShortNames {
			parts := strings.Fields(cmdStr)
			if len(parts) == 2 && parts[1] == resourceName {
				verb := parts[0]
				usedVerbs[verb] = true
				// Add a row for the verb with short name
				resourceRows = append(resourceRows, []string{serviceName, verb, resourceName, shortName})
			}
		}

		// Then add remaining verbs
		remainingVerbs := []string{}
		for _, verb := range verbs {
			if !usedVerbs[verb] {
				remainingVerbs = append(remainingVerbs, verb)
			}
		}

		if len(remainingVerbs) > 0 {
			resourceRows = append([][]string{{serviceName, strings.Join(remainingVerbs, ", "), resourceName, ""}}, resourceRows...)
		}

		resourceData[resourceName] = resourceRows
	}

	// Sort resources alphabetically
	var resources []string
	for resource := range resourceData {
		resources = append(resources, resource)
	}
	sort.Strings(resources)

	// Build final data array
	for _, resource := range resources {
		data = append(data, resourceData[resource]...)
	}

	return data, nil
}
