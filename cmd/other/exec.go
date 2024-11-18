package other

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/atotto/clipboard"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var parameters []string
var jsonParameter string
var fileParameter string
var apiVersion string
var outputFormat string
var copyToClipboard bool

// Config structure to parse environment files
type Config struct {
	Environment  string                 `yaml:"environment"`
	Environments map[string]Environment `yaml:"environments"`
}

type Environment struct {
	Endpoint string `yaml:"endpoint"`
	Proxy    bool   `yaml:"proxy"`
	Token    string `yaml:"token"`
}

var ExecCmd = &cobra.Command{
	Use:   "exec [rpc] [service].[resource]",
	Short: "Execute a gRPC request to a specified service and message",
	Long: `Executes a gRPC command to a given service and message based on environment configuration.
	For example: cfctl exec list identity.User`,
	Args: cobra.ExactArgs(2),
	Run:  runExecCommand,
}

func runExecCommand(cmd *cobra.Command, args []string) {
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	currentEnv, err := fetchCurrentEnvironment(config)
	if err != nil {
		log.Fatalf("Failed to get current environment: %v", err)
	}

	verbName := args[0]
	serviceResource := args[1]
	parts := strings.Split(serviceResource, ".")
	if len(parts) != 2 {
		log.Fatalf("Invalid service and resource format. Use [service].[resource]")
	}
	serviceName := parts[0]
	resourceName := parts[1]

	// Fetch endpoints map
	endpointsMap, err := fetchEndpointsMap(currentEnv.Endpoint)
	if err != nil {
		log.Fatalf("Failed to fetch endpoints map: %v", err)
	}

	endpoint, ok := endpointsMap[serviceName]
	if !ok {
		log.Fatalf("Service endpoint not found for service: %s", serviceName)
	}

	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		log.Fatalf("Invalid endpoint URL %s: %v", endpoint, err)
	}
	grpcEndpoint := fmt.Sprintf("%s:%s", parsedURL.Hostname(), parsedURL.Port())

	// Set up secure connection
	var opts []grpc.DialOption
	if parsedURL.Scheme == "grpc+ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(grpcEndpoint, opts...)
	if err != nil {
		log.Fatalf("Failed to connect to gRPC server: %v", err)
	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", currentEnv.Token)

	// Use Reflection to discover services
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Construct the full service name
	fullServiceName, err := discoverService(refClient, serviceName, resourceName)
	if err != nil {
		log.Fatalf("Failed to discover service: %v", err)
	}

	// Resolve the service and method
	serviceDesc, err := refClient.ResolveService(fullServiceName)
	if err != nil {
		log.Fatalf("Failed to resolve service %s: %v", fullServiceName, err)
	}

	methodDesc := serviceDesc.FindMethodByName(verbName)
	if methodDesc == nil {
		log.Fatalf("Method %s not found in service %s", verbName, fullServiceName)
	}

	// Create a dynamic message for the request
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

	// Parse the input parameters into the request message
	inputParams := parseParameters(fileParameter, jsonParameter, parameters)
	for key, value := range inputParams {
		if err := reqMsg.TrySetFieldByName(key, value); err != nil {
			log.Fatalf("Failed to set field %s: %v", key, err)
		}
	}

	// Prepare response placeholder
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	// Make the RPC call
	fullMethod := fmt.Sprintf("/%s/%s", fullServiceName, verbName)
	err = conn.Invoke(ctx, fullMethod, reqMsg, respMsg)
	if err != nil {
		log.Fatalf("Failed to call method %s: %v", verbName, err)
	}

	// Convert the response to a map
	respMap, err := messageToMap(respMsg)
	if err != nil {
		log.Fatalf("Failed to convert response message to map: %v", err)
	}

	jsonData, err := json.Marshal(respMap)
	if err != nil {
		log.Fatalf("Failed to marshal response to JSON: %v", err)
	}

	var prettyMap map[string]interface{}
	if err := json.Unmarshal(jsonData, &prettyMap); err != nil {
		log.Fatalf("Failed to unmarshal JSON data: %v", err)
	}

	printData(prettyMap, outputFormat)
}

func loadConfig() (*Config, error) {
	configPath := fmt.Sprintf("%s/.cfctl/config.yaml", os.Getenv("HOME"))
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

func fetchCurrentEnvironment(config *Config) (*Environment, error) {
	currentEnv, ok := config.Environments[config.Environment]
	if !ok {
		return nil, fmt.Errorf("current environment '%s' not found in config", config.Environment)
	}
	return &currentEnv, nil
}

func discoverService(refClient *grpcreflect.Client, serviceName, resourceName string) (string, error) {
	possibleVersions := []string{"v1", "v2"}

	for _, version := range possibleVersions {
		fullServiceName := fmt.Sprintf("spaceone.api.%s.%s.%s", serviceName, version, resourceName)
		if _, err := refClient.ResolveService(fullServiceName); err == nil {
			return fullServiceName, nil
		}
	}

	return "", fmt.Errorf("service not found for %s.%s", serviceName, resourceName)
}

func parseParameters(fileParameter, jsonParameter string, params []string) map[string]interface{} {
	parsed := make(map[string]interface{})

	// Load from file parameter if provided
	if fileParameter != "" {
		data, err := os.ReadFile(fileParameter)
		if err != nil {
			log.Fatalf("Failed to read file parameter: %v", err)
		}

		if err := yaml.Unmarshal(data, &parsed); err != nil {
			log.Fatalf("Failed to unmarshal YAML file: %v", err)
		}
	}

	// Load from JSON parameter if provided
	if jsonParameter != "" {
		if err := json.Unmarshal([]byte(jsonParameter), &parsed); err != nil {
			log.Fatalf("Failed to unmarshal JSON parameter: %v", err)
		}
	}

	// Parse key=value parameters
	for _, param := range params {
		parts := strings.SplitN(param, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("Invalid parameter format. Use key=value")
		}
		parsed[parts[0]] = parts[1]
	}

	return parsed
}

func messageToMap(msg *dynamic.Message) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	fields := msg.GetKnownFields()

	for _, fd := range fields {
		val := msg.GetField(fd)

		switch v := val.(type) {
		case *dynamic.Message:
			subMap, err := messageToMap(v)
			if err != nil {
				return nil, err
			}
			result[fd.GetName()] = subMap
		case []*dynamic.Message:
			var subList []map[string]interface{}
			for _, subMsg := range v {
				subMap, err := messageToMap(subMsg)
				if err != nil {
					return nil, err
				}
				subList = append(subList, subMap)
			}
			result[fd.GetName()] = subList
		case map[interface{}]interface{}:
			// Convert map[interface{}]interface{} to map[string]interface{}
			formattedMap := make(map[string]interface{})
			for key, value := range v {
				formattedMap[fmt.Sprintf("%v", key)] = value
			}
			result[fd.GetName()] = formattedMap
		case string:
			result[fd.GetName()] = v
		default:
			result[fd.GetName()] = v
		}
	}

	return result, nil
}

func printData(data map[string]interface{}, format string) {
	var output string

	switch format {
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
		output = printTable(data)

	case "csv":
		output = printCSV(data)

	default:
		log.Fatalf("Unsupported output format: %s", format)
	}

	// Copy to clipboard if requested
	if copyToClipboard && output != "" {
		if err := clipboard.WriteAll(output); err != nil {
			log.Fatalf("Failed to copy to clipboard: %v", err)
		}
		pterm.Success.Println("The output has been copied to your clipboard.")
	}
}

func printTable(data map[string]interface{}) string {
	var output string
	if results, ok := data["results"].([]interface{}); ok {
		tableData := pterm.TableData{}

		// Extract headers
		headers := []string{}
		if len(results) > 0 {
			if row, ok := results[0].(map[string]interface{}); ok {
				for key := range row {
					headers = append(headers, key)
				}
			}
		}

		// Append headers to table data
		tableData = append(tableData, headers)

		// Extract rows
		for _, result := range results {
			if row, ok := result.(map[string]interface{}); ok {
				rowData := []string{}
				for _, key := range headers {
					rowData = append(rowData, fmt.Sprintf("%v", row[key]))
				}
				tableData = append(tableData, rowData)
			}
		}

		// Disable styling only for the table output
		pterm.DisableStyling()
		renderedOutput, err := pterm.DefaultTable.WithHasHeader(true).WithData(tableData).Srender()
		pterm.EnableStyling() // Re-enable styling for other outputs
		if err != nil {
			log.Fatalf("Failed to render table: %v", err)
		}
		output = renderedOutput
		fmt.Println(output) // Print to console
	}
	return output
}

func printCSV(data map[string]interface{}) string {
	var buf bytes.Buffer
	if results, ok := data["results"].([]interface{}); ok {
		writer := csv.NewWriter(&buf)
		var headers []string

		// Extract headers
		for _, result := range results {
			if row, ok := result.(map[string]interface{}); ok {
				if headers == nil {
					for key := range row {
						headers = append(headers, key)
					}
					writer.Write(headers)
				}

				// Extract row values
				var rowValues []string
				for _, key := range headers {
					if val, ok := row[key]; ok {
						rowValues = append(rowValues, fmt.Sprintf("%v", val))
					} else {
						rowValues = append(rowValues, "")
					}
				}
				writer.Write(rowValues)
			}
		}

		writer.Flush()
		output := buf.String()
		fmt.Print(output) // Print to console
		return output
	}
	return ""
}

func init() {
	ExecCmd.Flags().StringArrayVarP(&parameters, "parameter", "p", []string{}, "Input Parameter (-p <key>=<value> -p ...)")
	ExecCmd.Flags().StringVarP(&jsonParameter, "json-parameter", "j", "", "JSON type parameter")
	ExecCmd.Flags().StringVarP(&fileParameter, "file-parameter", "f", "", "YAML file parameter")
	ExecCmd.Flags().StringVarP(&apiVersion, "api-version", "v", "v1", "API Version")
	ExecCmd.Flags().StringVarP(&outputFormat, "output", "o", "yaml", "Output format (yaml, json, table, csv)")
	ExecCmd.Flags().BoolVarP(&copyToClipboard, "copy", "c", false, "Copy the output to the clipboard (copies any output format)")
}
