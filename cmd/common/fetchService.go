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
	"strings"

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
	Token string `yaml:"token"`
}

// FetchService handles the execution of gRPC commands for all services
func FetchService(serviceName string, verb string, resourceName string) error {
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	jsonBytes, err := fetchJSONResponse(config, serviceName, verb, resourceName)
	if err != nil {
		return fmt.Errorf("failed to fetch JSON response: %v", err)
	}

	// Unmarshal JSON bytes to a map
	var respMap map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &respMap); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %v", err)
	}

	printData(respMap, outputFormat)

	return nil
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

func fetchJSONResponse(config *Config, serviceName string, verb string, resourceName string) ([]byte, error) {
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
