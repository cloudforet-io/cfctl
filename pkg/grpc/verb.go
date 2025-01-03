// common/fetchVerb.go

package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudforet-io/cfctl/cmd/other"
	"github.com/cloudforet-io/cfctl/pkg/format"
	"github.com/cloudforet-io/cfctl/pkg/settings"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"gopkg.in/yaml.v3"

	"github.com/spf13/cobra"
)

// FetchOptions holds the flag values for a command
type FetchOptions struct {
	Parameters      []string
	JSONParameter   string
	FileParameter   string
	APIVersion      string
	OutputFormat    string
	CopyToClipboard bool
	SortBy          string
	MinimalColumns  bool
	Columns         string
	Limit           int
	Page            int
	PageSize        int
}

// AddVerbCommands adds subcommands for each verb to the parent command
func AddVerbCommands(parentCmd *cobra.Command, serviceName string, groupID string) error {
	// Build the verb-resource map
	verbResourceMap, err := buildVerbResourceMap(serviceName)
	if err != nil {
		return nil // Return nil to prevent Cobra from showing additional error messages
	}

	// Get a sorted list of verbs
	verbs := make([]string, 0, len(verbResourceMap))
	for verb := range verbResourceMap {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)

	for _, verb := range verbs {
		currentVerb := verb
		resources := verbResourceMap[currentVerb]

		// Prepare Short and Long descriptions
		shortDesc := fmt.Sprintf("Supported %d resources", len(resources))

		verbCmd := &cobra.Command{
			Use:   currentVerb + " <resource>",
			Short: shortDesc,
			Long:  fmt.Sprintf("Supported %d resources for %s command.", len(resources), currentVerb),
			Args:  cobra.ArbitraryArgs, // Allow any number of arguments
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) != 1 {
					// Display the help message
					cmd.Help()
					return nil // Do not return an error to prevent additional error messages
				}
				resource := args[0]

				// Retrieve flag values
				parameters, err := cmd.Flags().GetStringArray("parameter")
				if err != nil {
					return err
				}
				jsonParameter, err := cmd.Flags().GetString("json-parameter")
				if err != nil {
					return err
				}
				fileParameter, err := cmd.Flags().GetString("file-parameter")
				if err != nil {
					return err
				}
				outputFormat, err := cmd.Flags().GetString("output")
				if err != nil {
					return err
				}
				copyToClipboard, err := cmd.Flags().GetBool("copy")
				if err != nil {
					return err
				}

				sortBy := ""
				columns := ""
				limit := 0
				pageSize := 100 // Default page size

				if currentVerb == "list" {
					sortBy, _ = cmd.Flags().GetString("sort")
					columns, _ = cmd.Flags().GetString("columns")
					limit, _ = cmd.Flags().GetInt("limit")
					pageSize, _ = cmd.Flags().GetInt("page-size")
				}

				options := &FetchOptions{
					Parameters:      parameters,
					JSONParameter:   jsonParameter,
					FileParameter:   fileParameter,
					OutputFormat:    outputFormat,
					CopyToClipboard: copyToClipboard,
					SortBy:          sortBy,
					MinimalColumns:  currentVerb == "list" && cmd.Flag("minimal") != nil && cmd.Flag("minimal").Changed,
					Columns:         columns,
					Limit:           limit,
					PageSize:        pageSize,
				}

				if currentVerb == "list" && !cmd.Flags().Changed("output") {
					options.OutputFormat = "table"
				}

				watch, _ := cmd.Flags().GetBool("watch")
				if watch && currentVerb == "list" {
					return watchResource(serviceName, currentVerb, resource, options)
				}

				_, err = FetchService(serviceName, currentVerb, resource, options)
				if err != nil {
					// Use pterm to display the error message
					pterm.Error.Println(err.Error())
					return nil // Return nil to prevent Cobra from displaying its own error message
				}
				return nil
			},
			GroupID: groupID,
			Annotations: map[string]string{
				"resources": strings.Join(resources, ", "),
			},
		}

		if currentVerb == "list" {
			verbCmd.Flags().BoolP("watch", "w", false, "Watch for changes")
			verbCmd.Flags().StringP("sort", "s", "", "Sort by field (e.g. 'name', 'created_at')")
			verbCmd.Flags().BoolP("minimal", "m", false, "Show minimal columns")
			verbCmd.Flags().StringP("columns", "c", "", "Specific columns (-c id,name)")
			verbCmd.Flags().IntP("limit", "l", 0, "Number of rows")
			verbCmd.Flags().IntP("page-size", "n", 15, "Number of items per page")
		}

		// Define flags for verbCmd
		verbCmd.Flags().StringArrayP("parameter", "p", []string{}, "Input Parameter (-p <key>=<value> -p ...)")
		verbCmd.Flags().StringP("json-parameter", "j", "", "JSON type parameter")
		verbCmd.Flags().StringP("file-parameter", "f", "", "YAML file parameter")
		verbCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml, json, table, csv)")
		verbCmd.Flags().BoolP("copy", "y", false, "Copy the output to the clipboard (copies any output format)")

		// Set custom help function
		verbCmd.SetHelpFunc(format.CustomVerbHelpFunc)

		// Update example for list command
		if currentVerb == "list" {
			verbCmd.Long = fmt.Sprintf("Supported %d resources for %s command.", len(resources), currentVerb)
		}

		parentCmd.AddCommand(verbCmd)
	}

	return nil
}

func buildVerbResourceMap(serviceName string) (map[string][]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	config, err := settings.LoadSetting()
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

func fetchVerbResourceMap(serviceName string, config *settings.Config) (map[string][]string, error) {
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
		identityEndpoint, hasIdentityService, err := other.GetIdentityEndpoint(apiEndpoint)

		if !hasIdentityService {
			// Get endpoints map first
			endpointsMap, err := other.FetchEndpointsMap(apiEndpoint)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch endpoints map: %v", err)
			}

			// Find the endpoint for the current service
			endpoint, exists := endpointsMap[serviceName]
			if !exists {
				return nil, fmt.Errorf("endpoint not found for service: %s", serviceName)
			}

			// Parse the endpoint
			parts := strings.Split(endpoint, "://")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
			}

			// Extract hostPort (remove the /v1 suffix if present)
			hostPort := strings.Split(parts[1], "/")[0]

			creds := credentials.NewTLS(tlsConfig)
			conn, err = grpc.Dial(hostPort, grpc.WithTransportCredentials(creds))
			if err != nil {
				return nil, fmt.Errorf("connection failed: %v", err)
			}
		} else {
			trimmedEndpoint := strings.TrimPrefix(identityEndpoint, "grpc+ssl://")
			parts := strings.Split(trimmedEndpoint, ".")
			if len(parts) < 4 {
				return nil, fmt.Errorf("invalid endpoint format: %s", trimmedEndpoint)
			}

			// Replace 'identity' with the converted service name
			parts[0] = format.ConvertServiceNameToEndpoint(serviceName)
			serviceEndpoint := strings.Join(parts, ".")

			creds := credentials.NewTLS(tlsConfig)
			conn, err = grpc.Dial(serviceEndpoint, grpc.WithTransportCredentials(creds))
			if err != nil {
				return nil, fmt.Errorf("connection failed: %v", err)
			}
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

// watchResource monitors a resource for changes and prints updates
func watchResource(serviceName, verb, resource string, options *FetchOptions) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	seenItems := make(map[string]bool)

	initialData, err := FetchService(serviceName, verb, resource, &FetchOptions{
		Parameters:      options.Parameters,
		JSONParameter:   options.JSONParameter,
		FileParameter:   options.FileParameter,
		APIVersion:      options.APIVersion,
		OutputFormat:    "",
		CopyToClipboard: false,
	})
	if err != nil {
		return err
	}

	if results, ok := initialData["results"].([]interface{}); ok {
		var recentItems []map[string]interface{}

		for _, item := range results {
			if m, ok := item.(map[string]interface{}); ok {
				identifier := generateIdentifier(m)
				seenItems[identifier] = true

				recentItems = append(recentItems, m)
				if len(recentItems) > 20 {
					recentItems = recentItems[1:]
				}
			}
		}

		if len(recentItems) > 0 {
			fmt.Printf("Recent items:\n")
			printNewItems(recentItems)
		}
	}

	fmt.Printf("\nWatching for changes... (Ctrl+C to quit)\n\n")

	for {
		select {
		case <-ticker.C:
			newData, err := FetchService(serviceName, verb, resource, &FetchOptions{
				Parameters:      options.Parameters,
				JSONParameter:   options.JSONParameter,
				FileParameter:   options.FileParameter,
				APIVersion:      options.APIVersion,
				OutputFormat:    "",
				CopyToClipboard: false,
			})
			if err != nil {
				continue
			}

			var newItems []map[string]interface{}
			if results, ok := newData["results"].([]interface{}); ok {
				for _, item := range results {
					if m, ok := item.(map[string]interface{}); ok {
						identifier := generateIdentifier(m)
						if !seenItems[identifier] {
							newItems = append(newItems, m)
							seenItems[identifier] = true
						}
					}
				}
			}

			if len(newItems) > 0 {
				fmt.Printf("Found %d new items at %s:\n",
					len(newItems),
					time.Now().Format("2006-01-02 15:04:05"))

				printNewItems(newItems)
				fmt.Println()
			}

		case <-sigChan:
			fmt.Println("\nStopping watch...")
			return nil
		}
	}
}

func generateIdentifier(item map[string]interface{}) string {
	if id, ok := item["job_task_id"]; ok {
		return fmt.Sprintf("%v", id)
	}

	var keys []string
	for k := range item {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%v=%v", k, item[k]))
	}
	return strings.Join(parts, ",")
}

func printNewItems(items []map[string]interface{}) {
	if len(items) == 0 {
		return
	}

	tableData := pterm.TableData{}

	headers := make([]string, 0)
	for key := range items[0] {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	tableData = append(tableData, headers)

	for _, item := range items {
		row := make([]string, len(headers))
		for i, header := range headers {
			if val, ok := item[header]; ok {
				row[i] = FormatTableValue(val)
			}
		}
		tableData = append(tableData, row)
	}

	pterm.DefaultTable.WithHasHeader().WithData(tableData).Render()
}
