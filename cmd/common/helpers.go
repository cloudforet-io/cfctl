// common/methods.go

package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudforet-io/cfctl/cmd/other"

	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"

	"github.com/pterm/pterm"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/jhump/protoreflect/grpcreflect"
)

func convertServiceNameToEndpoint(serviceName string) string {
	// cost_analysis -> cost-analysis
	// file_manager -> file-manager
	return strings.ReplaceAll(serviceName, "_", "-")
}

func BuildVerbResourceMap(serviceName string) (map[string][]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	if config.Environment == "local" {
		return handleLocalEnvironment(serviceName)
	}

	cacheDir := filepath.Join(home, ".cfctl", "cache", config.Environment)
	cacheFile := filepath.Join(cacheDir, "verb_resources.yaml")

	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < time.Hour {
			data, err := os.ReadFile(cacheFile)
			if err == nil {
				var allServices map[string]map[string][]string
				if err := yaml.Unmarshal(data, &allServices); err == nil {
					if verbMap, exists := allServices[serviceName]; exists {
						return verbMap, nil
					}
				}
			}
		}
	}

	verbResourceMap, err := fetchVerbResourceMap(serviceName, config)
	if err != nil {
		return nil, err
	}

	var allServices map[string]map[string][]string
	if data, err := os.ReadFile(cacheFile); err == nil {
		yaml.Unmarshal(data, &allServices)
	}
	if allServices == nil {
		allServices = make(map[string]map[string][]string)
	}

	allServices[serviceName] = verbResourceMap

	if err := os.MkdirAll(cacheDir, 0755); err == nil {
		data, err := yaml.Marshal(allServices)
		if err == nil {
			os.WriteFile(cacheFile, data, 0644)
		}
	}

	return verbResourceMap, nil
}

func handleLocalEnvironment(serviceName string) (map[string][]string, error) {
	// TODO: check services
	//if serviceName != "plugin" {
	//	return nil, fmt.Errorf("only plugin service is supported in local environment")
	//}

	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to local plugin service: %v", err)
	}
	defer conn.Close()

	ctx := context.Background()
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	services, err := refClient.ListServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list local services: %v", err)
	}

	verbResourceMap := make(map[string][]string)
	for _, s := range services {
		// Skip grpc reflection services
		if strings.HasPrefix(s, "grpc.") {
			continue
		}

		// Handle plugin service
		if serviceName == "plugin" && strings.Contains(s, ".plugin.") {
			serviceDesc, err := refClient.ResolveService(s)
			if err != nil {
				continue
			}

			resourceName := s[strings.LastIndex(s, ".")+1:]
			for _, method := range serviceDesc.GetMethods() {
				verb := method.GetName()
				if resources, ok := verbResourceMap[verb]; ok {
					verbResourceMap[verb] = append(resources, resourceName)
				} else {
					verbResourceMap[verb] = []string{resourceName}
				}
			}
			continue
		}

		// Handle other microservices
		if strings.Contains(s, fmt.Sprintf("spaceone.api.%s.", serviceName)) {
			serviceDesc, err := refClient.ResolveService(s)
			if err != nil {
				continue
			}

			resourceName := s[strings.LastIndex(s, ".")+1:]
			for _, method := range serviceDesc.GetMethods() {
				verb := method.GetName()
				if resources, ok := verbResourceMap[verb]; ok {
					verbResourceMap[verb] = append(resources, resourceName)
				} else {
					verbResourceMap[verb] = []string{resourceName}
				}
			}
		}
	}

	return verbResourceMap, nil
}

func fetchVerbResourceMap(serviceName string, config *Config) (map[string][]string, error) {
	envConfig := config.Environments[config.Environment]
	if envConfig.Endpoint == "" {
		return nil, fmt.Errorf("endpoint not found in environment config")
	}

	var conn *grpc.ClientConn
	var err error

	if config.Environment == "local" {
		endpoint := strings.TrimPrefix(envConfig.Endpoint, "grpc://")
		conn, err = grpc.Dial(endpoint, grpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("connection failed: %v", err)
		}
	} else {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		apiEndpoint, _ := other.GetAPIEndpoint(envConfig.Endpoint)
		identityEndpoint, _, err := other.GetIdentityEndpoint(apiEndpoint)

		trimmedEndpoint := strings.TrimPrefix(identityEndpoint, "grpc+ssl://")
		parts := strings.Split(trimmedEndpoint, ".")
		if len(parts) < 4 {
			return nil, fmt.Errorf("invalid endpoint format: %s", trimmedEndpoint)
		}

		// Replace 'identity' with the converted service name
		parts[0] = convertServiceNameToEndpoint(serviceName)

		creds := credentials.NewTLS(tlsConfig)
		conn, err = grpc.Dial(trimmedEndpoint, grpc.WithTransportCredentials(creds))
		if err != nil {
			return nil, fmt.Errorf("connection failed: %v", err)
		}

	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", envConfig.Token)
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	services, err := refClient.ListServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %v", err)
	}

	verbResourceMap := make(map[string][]string)
	for _, s := range services {
		if !strings.Contains(s, fmt.Sprintf(".%s.", serviceName)) {
			continue
		}

		serviceDesc, err := refClient.ResolveService(s)
		if err != nil {
			continue
		}

		resourceName := s[strings.LastIndex(s, ".")+1:]
		for _, method := range serviceDesc.GetMethods() {
			verb := method.GetName()
			if resources, ok := verbResourceMap[verb]; ok {
				verbResourceMap[verb] = append(resources, resourceName)
			} else {
				verbResourceMap[verb] = []string{resourceName}
			}
		}
	}

	return verbResourceMap, nil
}

// CustomParentHelpFunc customizes the help output for the parent command
func CustomParentHelpFunc(cmd *cobra.Command, args []string) {
	cmd.Printf("Usage:\n")
	cmd.Printf("  %s\n", cmd.UseLine())
	cmd.Printf("  %s\n\n", getAlternativeUsage(cmd))

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	printSortedBulletList(cmd, "Verbs")

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s <verb> --help\" for more information about a verb.\n", cmd.CommandPath())
	}
}

// PrintAvailableVerbs prints the available verbs along with both usage patterns
func PrintAvailableVerbs(cmd *cobra.Command) {
	cmd.Printf("Usage:\n")
	cmd.Printf("  %s\n", cmd.UseLine())
	cmd.Printf("  %s\n\n", getAlternativeUsage(cmd))

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	verbs := []string{}
	for _, subCmd := range cmd.Commands() {
		if !subCmd.Hidden {
			verbs = append(verbs, subCmd.Name())
		}
	}
	sort.Strings(verbs)
	pterm.Printf("Supported %d verbs\n", len(verbs))

	printSortedBulletList(cmd, "Verbs")

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s <verb> --help\" for more information about a verb.\n", cmd.CommandPath())
	}
}

// CustomVerbHelpFunc customizes the help output for verb commands
func CustomVerbHelpFunc(cmd *cobra.Command, args []string) {
	cmd.Printf("Usage:\n  %s\n\n", cmd.UseLine())

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	if resourcesStr, ok := cmd.Annotations["resources"]; ok && resourcesStr != "" {
		resources := strings.Split(resourcesStr, ", ")
		sort.Strings(resources)
		pterm.DefaultSection.Println("Resources")

		listItems := []pterm.BulletListItem{}
		for _, resource := range resources {
			listItems = append(listItems, pterm.BulletListItem{Level: 2, Text: resource})
		}
		pterm.DefaultBulletList.WithItems(listItems).Render()
		cmd.Println()
	}

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s <resource> --help\" for more information about a resource.\n", cmd.CommandPath())
	}
}

// getAlternativeUsage constructs the alternative usage string
func getAlternativeUsage(cmd *cobra.Command) string {
	// Extract the base command without flags
	baseCommand := cmd.CommandPath()
	return fmt.Sprintf("%s <verb> <resource> [flags]", baseCommand)
}

// printSortedBulletList prints a sorted bullet list under a given section title.
func printSortedBulletList(cmd *cobra.Command, sectionTitle string) {
	if len(cmd.Commands()) == 0 {
		return
	}

	pterm.DefaultSection.Println(sectionTitle)

	items := []string{}
	for _, subCmd := range cmd.Commands() {
		if !subCmd.Hidden {
			items = append(items, subCmd.Name())
		}
	}

	sort.Strings(items)

	listItems := make([]pterm.BulletListItem, len(items))
	for i, item := range items {
		listItems[i] = pterm.BulletListItem{Level: 2, Text: item}
	}

	pterm.DefaultBulletList.WithItems(listItems).Render()
	cmd.Println()
}
