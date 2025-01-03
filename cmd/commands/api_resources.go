package commands

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudforet-io/cfctl/pkg/settings"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
			return listAPIResources(serviceName)
		},
	}
}

func listAPIResources(serviceName string) error {
	setting, err := settings.LoadSetting()
	if err != nil {
		return fmt.Errorf("failed to load setting: %v", err)
	}

	endpoint, err := getServiceEndpoint(setting, serviceName)
	if err != nil {
		return fmt.Errorf("failed to get endpoint for service %s: %v", serviceName, err)
	}

	shortNamesMap, err := loadShortNames()
	if err != nil {
		return fmt.Errorf("failed to load short names: %v", err)
	}

	data, err := fetchServiceResources(serviceName, endpoint, shortNamesMap, setting)
	if err != nil {
		return fmt.Errorf("failed to fetch resources for service %s: %v", serviceName, err)
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	renderAPITable(data)

	return nil
}

func getServiceEndpoint(config *settings.Config, serviceName string) (string, error) {
	envConfig := config.Environments[config.Environment]
	if envConfig.URL == "" {
		return "", fmt.Errorf("URL not found in environment config")
	}

	// Parse URL to get environment
	urlParts := strings.Split(envConfig.URL, ".")
	if len(urlParts) < 4 {
		return "", fmt.Errorf("invalid URL format: %s", envConfig.URL)
	}

	var envPrefix string
	for i, part := range urlParts {
		if part == "console" && i+1 < len(urlParts) {
			envPrefix = urlParts[i+1] // Get the part after "console" (dev or stg)
			break
		}
	}

	if envPrefix == "" {
		return "", fmt.Errorf("environment prefix not found in URL: %s", envConfig.URL)
	}

	endpoint := fmt.Sprintf("grpc+ssl://%s.api.%s.spaceone.dev:443", serviceName, envPrefix)
	return endpoint, nil
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

func fetchServiceResources(serviceName, endpoint string, shortNamesMap map[string]string, config *settings.Config) ([][]string, error) {
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
		if !strings.Contains(s, fmt.Sprintf(".%s.", serviceName)) {
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

func renderAPITable(data [][]string) {
	// Create table header
	table := pterm.TableData{
		{"Service", "Verb", "Resource", "Short Names"},
	}

	// Add data rows
	table = append(table, data...)

	// Render the table
	pterm.DefaultTable.WithHasHeader().WithData(table).Render()
}
