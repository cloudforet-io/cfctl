// common/apiResources.go

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

	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"gopkg.in/yaml.v3"
)

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

func ListAPIResources(serviceName string) error {
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	endpoint, err := getServiceEndpoint(config, serviceName)
	if err != nil {
		return fmt.Errorf("failed to get endpoint for service %s: %v", serviceName, err)
	}

	shortNamesMap, err := loadShortNames()
	if err != nil {
		return fmt.Errorf("failed to load short names: %v", err)
	}

	data, err := fetchServiceResources(serviceName, endpoint, shortNamesMap, config)
	if err != nil {
		return fmt.Errorf("failed to fetch resources for service %s: %v", serviceName, err)
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i][0] < data[j][0]
	})

	renderAPITable(data)

	return nil
}

func getServiceEndpoint(config *Config, serviceName string) (string, error) {
	var envPrefix string
	if strings.HasPrefix(config.Environment, "dev-") {
		envPrefix = "dev"
	} else if strings.HasPrefix(config.Environment, "stg-") {
		envPrefix = "stg"
	} else {
		return "", fmt.Errorf("unsupported environment prefix")
	}
	endpoint := fmt.Sprintf("grpc+ssl://%s.api.%s.spaceone.dev:443", serviceName, envPrefix)
	return endpoint, nil
}

func fetchServiceResources(serviceName, endpoint string, shortNamesMap map[string]string, config *Config) ([][]string, error) {
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

	data := [][]string{}
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
		shortName := shortNamesMap[fmt.Sprintf("%s.%s", serviceName, resourceName)]

		verbs := []string{}
		for _, method := range serviceDesc.GetMethods() {
			verbs = append(verbs, method.GetName())
		}

		data = append(data, []string{strings.Join(verbs, ", "), resourceName, shortName})
	}

	return data, nil
}

func renderAPITable(data [][]string) {
	// Sort the data by the 'Resource' column alphabetically
	sort.Slice(data, func(i, j int) bool {
		return data[i][1] < data[j][1]
	})

	// Calculate the terminal width
	terminalWidth, _, err := pterm.GetTerminalSize()
	if err != nil {
		terminalWidth = 80 // Default width if unable to get terminal size
	}

	// Define the minimum widths for the columns
	minResourceWidth := 15
	minShortNameWidth := 15
	padding := 5 // Padding between columns and borders

	// Calculate the available width for the Verb column
	verbColumnWidth := terminalWidth - (minResourceWidth + minShortNameWidth + padding)
	if verbColumnWidth < 20 {
		verbColumnWidth = 20 // Minimum width for the Verb column
	}

	// Prepare the table data with headers
	table := pterm.TableData{{"Verb", "Resource", "Short Names"}}

	for _, row := range data {
		verbs := row[0]
		resource := row[1]
		shortName := row[2]

		// Wrap the verbs text based on the calculated column width
		wrappedVerbs := wordWrap(verbs, verbColumnWidth)

		// Build the table row
		table = append(table, []string{wrappedVerbs, resource, shortName})
	}

	// Render the table using pterm with separators
	pterm.DefaultTable.WithHasHeader().
		WithRowSeparator("-").
		WithHeaderRowSeparator("-").
		WithLeftAlignment().
		WithData(table).
		Render()
}

// wordWrap function remains the same
func wordWrap(text string, width int) string {
	var wrappedText string
	var line string
	words := strings.Fields(text) // Split on spaces only

	for _, word := range words {
		if len(line)+len(word)+1 > width {
			if wrappedText != "" {
				wrappedText += "\n"
			}
			wrappedText += line
			line = word
		} else {
			if line != "" {
				line += " "
			}
			line += word
		}
	}
	if line != "" {
		if wrappedText != "" {
			wrappedText += "\n"
		}
		wrappedText += line
	}

	return wrappedText
}
