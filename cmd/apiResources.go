// apiResources.go

package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"

	"github.com/pterm/pterm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

var endpoints string

var apiResourcesCmd = &cobra.Command{
	Use:   "api-resources",
	Short: "Displays supported API resources",
	Run: func(cmd *cobra.Command, args []string) {
		// Load the main configuration file
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Unable to find home directory: %v", err)
		}

		configFile := filepath.Join(home, ".spaceone", "config.yaml")
		viper.SetConfigFile(configFile)
		if err := viper.ReadInConfig(); err != nil {
			log.Fatalf("Error reading config file: %v", err)
		}

		// Get the current environment
		currentEnv := viper.GetString("environment")
		if currentEnv == "" {
			log.Fatalf("No active environment specified in %s", configFile)
		}

		// Get environment-specific configurations
		envConfig := viper.Sub(fmt.Sprintf("environments.%s", currentEnv))
		if envConfig == nil {
			log.Fatalf("No configuration found for environment: %s", currentEnv)
		}

		endpoint := envConfig.GetString("endpoint")
		proxy := envConfig.GetBool("proxy")

		// Validate endpoint and proxy
		if !proxy || !strings.Contains(endpoint, "identity") {
			log.Fatalf("Endpoint for environment '%s' is not valid for fetching resources.", currentEnv)
		}

		// Fetch endpointsMap dynamically
		endpointsMap, err := fetchEndpointsMap(endpoint)
		if err != nil {
			log.Fatalf("Failed to fetch endpointsMap from '%s': %v", endpoint, err)
		}

		// Load short names configuration
		shortNamesFile := filepath.Join(home, ".spaceone", "short_names.yaml")
		shortNamesMap := make(map[string]string)
		if _, err := os.Stat(shortNamesFile); err == nil {
			file, err := os.Open(shortNamesFile)
			if err != nil {
				log.Fatalf("Failed to open short_names.yaml file: %v", err)
			}
			defer file.Close()

			err = yaml.NewDecoder(file).Decode(&shortNamesMap)
			if err != nil {
				log.Fatalf("Failed to decode short_names.yaml: %v", err)
			}
		}

		// Process endpoints provided via flag
		if endpoints != "" {
			selectedEndpoints := strings.Split(endpoints, ",")
			for i := range selectedEndpoints {
				selectedEndpoints[i] = strings.TrimSpace(selectedEndpoints[i])
			}
			var allData [][]string

			for _, endpointName := range selectedEndpoints {
				serviceEndpoint, ok := endpointsMap[endpointName]
				if !ok {
					log.Printf("No endpoint found for %s", endpointName)
					continue
				}

				result, err := fetchServiceResources(endpointName, serviceEndpoint, shortNamesMap)
				if err != nil {
					log.Printf("Error processing service %s: %v", endpointName, err)
					continue
				}

				allData = append(allData, result...)
			}

			sort.Slice(allData, func(i, j int) bool {
				return allData[i][0] < allData[j][0]
			})

			renderTable(allData)
			return
		}

		// If no specific endpoints are provided, list all services
		var wg sync.WaitGroup
		dataChan := make(chan [][]string, len(endpointsMap))
		errorChan := make(chan error, len(endpointsMap))

		for service, endpoint := range endpointsMap {
			wg.Add(1)
			go func(service, endpoint string) {
				defer wg.Done()
				result, err := fetchServiceResources(service, endpoint, shortNamesMap)
				if err != nil {
					errorChan <- fmt.Errorf("Error processing service %s: %v", service, err)
					return
				}
				dataChan <- result
			}(service, endpoint)
		}

		wg.Wait()
		close(dataChan)
		close(errorChan)

		if len(errorChan) > 0 {
			for err := range errorChan {
				log.Println(err)
			}
		}

		var allData [][]string
		for data := range dataChan {
			allData = append(allData, data...)
		}

		sort.Slice(allData, func(i, j int) bool {
			return allData[i][0] < allData[j][0]
		})

		renderTable(allData)
	},
}

func fetchEndpointsMap(endpoint string) (map[string]string, error) {
	// Configure gRPC connection
	parts := strings.Split(endpoint, "://")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
	}

	scheme := parts[0]
	hostPort := strings.SplitN(parts[1], "/", 2)[0]

	var opts []grpc.DialOption
	if scheme == "grpc+ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false, // Enable server certificate verification
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

	// Replace this with your gRPC client and request logic
	// Example assumes a gRPC client `IdentityClient` with `ListEndpoints` RPC
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

	// Convert response to map
	endpointsMap := make(map[string]string)
	for _, s := range resp.GetListServicesResponse().Service {
		endpointsMap[s.Name] = s.Name // Replace with appropriate key-value parsing
	}

	return endpointsMap, nil
}

func fetchServiceResources(service, endpoint string, shortNamesMap map[string]string) ([][]string, error) {
	// Configure gRPC connection based on TLS usage
	parts := strings.Split(endpoint, "://")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
	}

	scheme := parts[0]
	hostPort := strings.SplitN(parts[1], "/", 2)[0]

	var opts []grpc.DialOption
	if scheme == "grpc+ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false, // Enable server certificate verification
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

	// List all services
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
	data := [][]string{}
	for _, s := range services {
		if strings.HasPrefix(s.Name, "grpc.reflection.v1alpha.") {
			continue
		}
		resourceName := s.Name[strings.LastIndex(s.Name, ".")+1:]
		verbs := getServiceMethods(client, s.Name)
		shortName := shortNamesMap[fmt.Sprintf("%s.%s", service, resourceName)]
		data = append(data, []string{service, resourceName, shortName, strings.Join(verbs, ", ")})
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

func getConfigDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	return filepath.Join(home, ".spaceone")
}

func renderTable(data [][]string) {
	// Calculate the dynamic width for the "Verb" column
	terminalWidth := pterm.GetTerminalWidth()
	usedWidth := 30 + 20 + 15 // Estimated widths for Service, Resource, and Short Names
	verbColumnWidth := terminalWidth - usedWidth
	if verbColumnWidth < 20 {
		verbColumnWidth = 20 // Minimum width for Verb column
	}

	// Use a more extensive color palette to ensure distinct colors for each service
	serviceColors := []pterm.Color{
		pterm.FgLightGreen, pterm.FgLightYellow, pterm.FgLightBlue,
		pterm.FgLightMagenta, pterm.FgLightCyan, pterm.FgWhite,
		pterm.FgGreen, pterm.FgYellow, pterm.FgBlue,
		pterm.FgMagenta, pterm.FgCyan, pterm.FgLightWhite,
		pterm.FgRed, pterm.FgLightRed, pterm.FgLightYellow,
		pterm.FgLightGreen, pterm.FgDarkGray, pterm.FgLightMagenta,
	}

	serviceColorMap := make(map[string]pterm.Color)
	colorIndex := 0

	table := pterm.TableData{{"Service", "Resource", "Short Names", "Verb"}}

	for _, row := range data {
		service := row[0]
		// Assign a unique color to each service if not already assigned
		if _, exists := serviceColorMap[service]; !exists {
			serviceColorMap[service] = serviceColors[colorIndex]
			colorIndex = (colorIndex + 1) % len(serviceColors) // Cycle through the colors if necessary
		}

		// Get the color for this service
		color := serviceColorMap[service]
		coloredStyle := pterm.NewStyle(color)

		// Color the entire row (Service, Resource, Short Names, Verb)
		serviceColored := coloredStyle.Sprint(service)
		resourceColored := coloredStyle.Sprint(row[1])
		shortNamesColored := coloredStyle.Sprint(row[2])

		verbs := splitIntoLinesWithComma(row[3], verbColumnWidth)
		for i, line := range verbs {
			if i == 0 {
				table = append(table, []string{serviceColored, resourceColored, shortNamesColored, coloredStyle.Sprint(line)})
			} else {
				table = append(table, []string{"", "", "", coloredStyle.Sprint(line)})
			}
		}
	}

	// Render the table using pterm
	pterm.DefaultTable.WithHasHeader().WithData(table).Render()
}

func splitIntoLinesWithComma(text string, maxWidth int) []string {
	words := strings.Split(text, ", ")
	var lines []string
	var currentLine string

	for _, word := range words {
		if len(currentLine)+len(word)+2 > maxWidth { // +2 accounts for the ", " separator
			lines = append(lines, currentLine+",")
			currentLine = word
		} else {
			if currentLine != "" {
				currentLine += ", "
			}
			currentLine += word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

func init() {
	rootCmd.AddCommand(apiResourcesCmd)
	apiResourcesCmd.Flags().StringVarP(&endpoints, "service", "s", "", "Specify the services to connect to, separated by commas (e.g., 'identity', 'identity,inventory')")
}
