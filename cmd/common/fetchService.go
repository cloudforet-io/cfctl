package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eiannone/keyboard"
	"github.com/spf13/viper"

	"github.com/atotto/clipboard"
	"github.com/pterm/pterm"

	"google.golang.org/grpc/metadata"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"gopkg.in/yaml.v3"
)

// Config structure to parse environment files
type Config struct {
	Environment  string                 `yaml:"environment"`
	Environments map[string]Environment `yaml:"environments"`
}

type Environment struct {
	Endpoint string `yaml:"endpoint"`
	Proxy    string `yaml:"proxy"`
	Token    string `yaml:"token"`
}

// FetchService handles the execution of gRPC commands for all services
func FetchService(serviceName string, verb string, resourceName string, options *FetchOptions) (map[string]interface{}, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Read configuration file
	mainViper := viper.New()
	mainViper.SetConfigFile(filepath.Join(homeDir, ".cfctl", "setting.toml"))
	mainViper.SetConfigType("toml")
	if err := mainViper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read configuration file. Please run 'cfctl login' first")
	}

	// Check current environment
	currentEnv := mainViper.GetString("environment")
	if currentEnv == "" {
		return nil, fmt.Errorf("no environment set. Please run 'cfctl login' first")
	}

	// Load configuration first
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	// Check token
	token := config.Environments[config.Environment].Token
	if token == "" {
		pterm.Error.Println("No token found for authentication.")

		// Get current endpoint
		endpoint := config.Environments[config.Environment].Endpoint

		if strings.HasSuffix(config.Environment, "-app") {
			// App environment message
			headerBox := pterm.DefaultBox.WithTitle("App Guide").
				WithTitleTopCenter().
				WithRightPadding(4).
				WithLeftPadding(4).
				WithBoxStyle(pterm.NewStyle(pterm.FgLightCyan))

			appTokenExplain := "Please create a Domain Admin App in SpaceONE Console.\n" +
				"This requires Domain Admin privilege.\n\n" +
				"Or Please create a Workspace App in SpaceONE Console.\n" +
				"This requires Workspace Owner privilege."

			pterm.Info.Printf("Using endpoint: %s\n", endpoint)
			headerBox.Println(appTokenExplain)
			fmt.Println()

			steps := []string{
				"1. Go to SpaceONE Console",
				"2. Navigate to either 'Admin > App Page' or specific 'Workspace > App page'",
				"3. Click 'Create' to create your App",
				"4. Copy value of either 'client_secret' from Client ID or 'token' from Spacectl (CLI)",
			}

			yamlExample := pterm.DefaultBox.WithTitle("Config Example").
				WithTitleTopCenter().
				WithRightPadding(4).
				WithLeftPadding(4).
				Sprint(fmt.Sprintf("environment: %s\nenvironments:\n    %s:\n        endpoint: %s\n        proxy: true\n        token: %s",
					currentEnv,
					currentEnv,
					endpoint,
					pterm.FgLightCyan.Sprint("YOUR_COPIED_TOKEN")))

			instructionBox := pterm.DefaultBox.WithTitle("Required Steps").
				WithTitleTopCenter().
				WithRightPadding(4).
				WithLeftPadding(4)

			allSteps := append(steps,
				fmt.Sprintf("5. Add the token under the proxy in your config file:\n%s", yamlExample),
				"6. Run 'cfctl login' again")

			instructionBox.Println(strings.Join(allSteps, "\n\n"))
		} else {
			// User environment message
			headerBox := pterm.DefaultBox.WithTitle("Authentication Required").
				WithTitleTopCenter().
				WithRightPadding(4).
				WithLeftPadding(4).
				WithBoxStyle(pterm.NewStyle(pterm.FgLightCyan))

			authExplain := "Please login to SpaceONE Console first.\n" +
				"This requires your SpaceONE credentials."

			headerBox.Println(authExplain)
			fmt.Println()

			steps := []string{
				"1. Run 'cfctl login'",
				"2. Enter your credentials when prompted",
				"3. Select your workspace",
				"4. Try your command again",
			}

			instructionBox := pterm.DefaultBox.WithTitle("Required Steps").
				WithTitleTopCenter().
				WithRightPadding(4).
				WithLeftPadding(4)

			instructionBox.Println(strings.Join(steps, "\n\n"))
		}

		return nil, nil
	}

	jsonBytes, err := fetchJSONResponse(config, serviceName, verb, resourceName, options)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON bytes to a map
	var respMap map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &respMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %v", err)
	}

	// Print the data if not in watch mode
	if options.OutputFormat != "" {
		printData(respMap, options)
	}

	return respMap, nil
}

func loadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	// Load main configuration file
	mainV := viper.New()
	mainConfigPath := filepath.Join(home, ".cfctl", "setting.toml")
	mainV.SetConfigFile(mainConfigPath)
	mainV.SetConfigType("toml")
	if err := mainV.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	currentEnv := mainV.GetString("environment")
	if currentEnv == "" {
		return nil, fmt.Errorf("no environment set in config")
	}

	// Get environment config from main config file
	envConfig := &Environment{
		Endpoint: mainV.GetString(fmt.Sprintf("environments.%s.endpoint", currentEnv)),
		Proxy:    mainV.GetString(fmt.Sprintf("environments.%s.proxy", currentEnv)),
	}

	// Handle token based on environment type
	if strings.HasSuffix(currentEnv, "-user") {
		// For user environments, read from grant_token file
		grantTokenPath := filepath.Join(home, ".cfctl", "cache", currentEnv, "grant_token")
		tokenBytes, err := os.ReadFile(grantTokenPath)
		if err == nil {
			envConfig.Token = strings.TrimSpace(string(tokenBytes))
		}
	} else if strings.HasSuffix(currentEnv, "-app") {
		// For app environments, get token from main config
		envConfig.Token = mainV.GetString(fmt.Sprintf("environments.%s.token", currentEnv))
	}

	if envConfig == nil {
		return nil, fmt.Errorf("environment '%s' not found in config files", currentEnv)
	}

	return &Config{
		Environment: currentEnv,
		Environments: map[string]Environment{
			currentEnv: *envConfig,
		},
	}, nil
}

func fetchJSONResponse(config *Config, serviceName string, verb string, resourceName string, options *FetchOptions) ([]byte, error) {
	var envPrefix string
	if strings.HasPrefix(config.Environment, "dev-") {
		envPrefix = "dev"
	} else if strings.HasPrefix(config.Environment, "stg-") {
		envPrefix = "stg"
	}
	hostPort := fmt.Sprintf("%s.api.%s.spaceone.dev:443", serviceName, envPrefix)

	// Configure gRPC connection
	var opts []grpc.DialOption
	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}
	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.WithTransportCredentials(creds))

	opts = append(opts, grpc.WithDefaultCallOptions(
		grpc.MaxCallRecvMsgSize(10*1024*1024), // 10MB
		grpc.MaxCallSendMsgSize(10*1024*1024), // 10MB
	))

	// Establish the connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", hostPort, err)
	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", config.Environments[config.Environment].Token)
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	fullServiceName, err := discoverService(refClient, serviceName, resourceName)
	if err != nil {
		return nil, fmt.Errorf("failed to discover service: %v", err)
	}

	serviceDesc, err := refClient.ResolveService(fullServiceName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service %s: %v", fullServiceName, err)
	}

	methodDesc := serviceDesc.FindMethodByName(verb)
	if methodDesc == nil {
		return nil, fmt.Errorf("method not found: %s", verb)
	}

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	// Parse the input parameters into the request message
	inputParams, err := parseParameters(options)
	if err != nil {
		return nil, err
	}

	for key, value := range inputParams {
		if err := reqMsg.TrySetFieldByName(key, value); err != nil {
			// If the error indicates an unknown field, list valid fields
			if strings.Contains(err.Error(), "unknown field") {
				validFields := []string{}
				fieldDescs := reqMsg.GetKnownFields()
				for _, fd := range fieldDescs {
					validFields = append(validFields, fd.GetName())
				}
				return nil, fmt.Errorf("failed to set field '%s': unknown field name. Valid fields are: %s", key, strings.Join(validFields, ", "))
			}
			return nil, fmt.Errorf("failed to set field '%s': %v", key, err)
		}
	}

	fullMethod := fmt.Sprintf("/%s/%s", fullServiceName, verb)

	err = conn.Invoke(ctx, fullMethod, reqMsg, respMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method %s: %v", fullMethod, err)
	}

	jsonBytes, err := respMsg.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response message to JSON: %v", err)
	}

	return jsonBytes, nil
}

func parseParameters(options *FetchOptions) (map[string]interface{}, error) {
	parsed := make(map[string]interface{})

	// Load from file parameter if provided
	if options.FileParameter != "" {
		data, err := os.ReadFile(options.FileParameter)
		if err != nil {
			return nil, fmt.Errorf("failed to read file parameter: %v", err)
		}

		if err := yaml.Unmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("failed to unmarshal YAML file: %v", err)
		}
	}

	// Load from JSON parameter if provided
	if options.JSONParameter != "" {
		if err := json.Unmarshal([]byte(options.JSONParameter), &parsed); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON parameter: %v", err)
		}
	}

	// Parse key=value parameters
	for _, param := range options.Parameters {
		parts := strings.SplitN(param, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid parameter format. Use key=value")
		}
		key := parts[0]
		value := parts[1]

		// Attempt to parse value as JSON
		var jsonValue interface{}
		if err := json.Unmarshal([]byte(value), &jsonValue); err == nil {
			parsed[key] = jsonValue
		} else {
			parsed[key] = value
		}
	}

	return parsed, nil
}

func discoverService(refClient *grpcreflect.Client, serviceName string, resourceName string) (string, error) {
	possibleVersions := []string{"v1", "v2"}

	for _, version := range possibleVersions {
		fullServiceName := fmt.Sprintf("spaceone.api.%s.%s.%s", serviceName, version, resourceName)
		if _, err := refClient.ResolveService(fullServiceName); err == nil {
			return fullServiceName, nil
		}
	}

	return "", fmt.Errorf("service not found for %s.%s", serviceName, resourceName)
}

func printData(data map[string]interface{}, options *FetchOptions) {
	var output string

	switch options.OutputFormat {
	case "json":
		dataBytes, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal response to JSON: %v", err)
		}
		output = string(dataBytes)
		fmt.Println(output)

	case "yaml":
		var buf bytes.Buffer
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)
		err := encoder.Encode(data)
		if err != nil {
			log.Fatalf("Failed to marshal response to YAML: %v", err)
		}
		output = buf.String()
		fmt.Printf("---\n%s\n", output)

	case "table":
		// Check if data has 'results' key
		if _, ok := data["results"].([]interface{}); ok {
			output = printTable(data)
		} else {
			// If no 'results' key, treat the entire data as results
			wrappedData := map[string]interface{}{
				"results": []interface{}{data},
			}
			output = printTable(wrappedData)
		}

	case "csv":
		output = printCSV(data)

	default:
		var buf bytes.Buffer
		encoder := yaml.NewEncoder(&buf)
		encoder.SetIndent(2)
		err := encoder.Encode(data)
		if err != nil {
			log.Fatalf("Failed to marshal response to YAML: %v", err)
		}
		output = buf.String()
		fmt.Printf("---\n%s\n", output)
	}

	// Copy to clipboard if requested
	if options.CopyToClipboard && output != "" {
		if err := clipboard.WriteAll(output); err != nil {
			log.Fatalf("Failed to copy to clipboard: %v", err)
		}
		pterm.Success.Println("The output has been copied to your clipboard.")
	}
}

func printTable(data map[string]interface{}) string {
	if results, ok := data["results"].([]interface{}); ok {
		pageSize := 10
		currentPage := 0
		searchTerm := ""
		filteredResults := results

		// Initialize keyboard
		if err := keyboard.Open(); err != nil {
			fmt.Println("Failed to initialize keyboard:", err)
			return ""
		}
		defer keyboard.Close()

		// Extract headers
		headers := []string{}
		if len(results) > 0 {
			if row, ok := results[0].(map[string]interface{}); ok {
				for key := range row {
					headers = append(headers, key)
				}
				sort.Strings(headers)
			}
		}

		for {
			if searchTerm != "" {
				filteredResults = filterResults(results, searchTerm)
			} else {
				filteredResults = results
			}

			totalItems := len(filteredResults)
			totalPages := (totalItems + pageSize - 1) / pageSize

			tableData := pterm.TableData{headers}

			// Calculate current page items
			startIdx := currentPage * pageSize
			endIdx := startIdx + pageSize
			if endIdx > totalItems {
				endIdx = totalItems
			}

			// Clear screen
			fmt.Print("\033[H\033[2J")

			if searchTerm != "" {
				fmt.Printf("Search: %s (Found: %d items)\n", searchTerm, totalItems)
			}

			// Add rows for current page using filteredResults
			for _, result := range filteredResults[startIdx:endIdx] {
				if row, ok := result.(map[string]interface{}); ok {
					rowData := make([]string, len(headers))
					for i, key := range headers {
						rowData[i] = formatTableValue(row[key])
					}
					tableData = append(tableData, rowData)
				}
			}

			// Clear screen
			fmt.Print("\033[H\033[2J")

			// Print table
			pterm.DefaultTable.WithHasHeader().WithData(tableData).Render()

			fmt.Printf("\nPage %d of %d (Total items: %d)\n", currentPage+1, totalPages, totalItems)
			fmt.Println("Navigation: [h]previous page, [l]next page, [/]search, [c]lear search, [q]uit")

			// Get keyboard input
			char, _, err := keyboard.GetKey()
			if err != nil {
				fmt.Println("Error reading keyboard input:", err)
				return ""
			}

			switch char {
			case 'l', 'L':
				if currentPage < totalPages-1 {
					currentPage++
				} else {
					currentPage = 0
				}
			case 'h', 'H':
				if currentPage > 0 {
					currentPage--
				} else {
					currentPage = totalPages - 1
				}
			case 'q', 'Q':
				return ""
			case 'c', 'C':
				searchTerm = ""
				currentPage = 0
			case '/':
				fmt.Print("\nEnter search term: ")
				keyboard.Close()
				var input string
				fmt.Scanln(&input)
				searchTerm = input
				currentPage = 0
				keyboard.Open()
			}
		}
	} else {
		headers := make([]string, 0)
		for key := range data {
			headers = append(headers, key)
		}
		sort.Strings(headers)

		tableData := pterm.TableData{
			{"Field", "Value"},
		}

		for _, header := range headers {
			value := formatTableValue(data[header])
			tableData = append(tableData, []string{header, value})
		}

		pterm.DefaultTable.WithHasHeader().WithData(tableData).Render()
	}
	return ""
}

func filterResults(results []interface{}, searchTerm string) []interface{} {
	var filtered []interface{}
	searchTerm = strings.ToLower(searchTerm)

	for _, result := range results {
		if row, ok := result.(map[string]interface{}); ok {
			for _, value := range row {
				strValue := strings.ToLower(fmt.Sprintf("%v", value))
				if strings.Contains(strValue, searchTerm) {
					filtered = append(filtered, result)
					break
				}
			}
		}
	}
	return filtered
}

func formatTableValue(val interface{}) string {
	switch v := val.(type) {
	case nil:
		return ""
	case string:
		// Add colors for status values
		switch strings.ToUpper(v) {
		case "SUCCESS":
			return pterm.FgGreen.Sprint(v)
		case "FAILURE":
			return pterm.FgRed.Sprint(v)
		case "PENDING":
			return pterm.FgYellow.Sprint(v)
		case "RUNNING":
			return pterm.FgBlue.Sprint(v)
		default:
			return v
		}
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%v", v)
	case map[string]interface{}, []interface{}:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(jsonBytes)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func printCSV(data map[string]interface{}) string {
	// CSV writer 생성
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	if results, ok := data["results"].([]interface{}); ok {
		if len(results) == 0 {
			return ""
		}

		headers := make([]string, 0)
		if firstRow, ok := results[0].(map[string]interface{}); ok {
			for key := range firstRow {
				headers = append(headers, key)
			}
			sort.Strings(headers)
			writer.Write(headers)
		}

		for _, result := range results {
			if row, ok := result.(map[string]interface{}); ok {
				rowData := make([]string, len(headers))
				for i, header := range headers {
					rowData[i] = formatTableValue(row[header])
				}
				writer.Write(rowData)
			}
		}
	} else {
		headers := []string{"Field", "Value"}
		writer.Write(headers)

		fields := make([]string, 0)
		for field := range data {
			fields = append(fields, field)
		}
		sort.Strings(fields)

		for _, field := range fields {
			row := []string{field, formatTableValue(data[field])}
			writer.Write(row)
		}
	}

	return ""
}

func formatCSVValue(val interface{}) string {
	switch v := val.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%v", v)
	case map[string]interface{}, []interface{}:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(jsonBytes)
	default:
		return fmt.Sprintf("%v", v)
	}
}
