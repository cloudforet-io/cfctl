// common/methods.go

package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pterm/pterm"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/jhump/protoreflect/grpcreflect"
)

// BuildVerbResourceMap builds a mapping from verbs to resources for a given service
func BuildVerbResourceMap(serviceName string) (map[string][]string, error) {
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

	verbResourceMap := make(map[string]map[string]struct{})

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

		// Extract the resource name from the service name
		parts := strings.Split(s, ".")
		resourceName := parts[len(parts)-1]

		for _, method := range serviceDesc.GetMethods() {
			verb := method.GetName()
			if verbResourceMap[verb] == nil {
				verbResourceMap[verb] = make(map[string]struct{})
			}
			verbResourceMap[verb][resourceName] = struct{}{}
		}
	}

	// Convert the map of resources to slices
	result := make(map[string][]string)
	for verb, resourcesSet := range verbResourceMap {
		resources := []string{}
		for resource := range resourcesSet {
			resources = append(resources, resource)
		}
		sort.Strings(resources)
		result[verb] = resources
	}

	return result, nil
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
