// apiResources.go

package other

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"

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

func loadEndpointsFromCache(currentEnv string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}

	// Read from environment-specific cache file
	cacheFile := filepath.Join(home, ".cfctl", "cache", currentEnv, "endpoints.toml")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	var endpoints map[string]string
	if err := toml.Unmarshal(data, &endpoints); err != nil {
		return nil, err
	}

	return endpoints, nil
}

var ApiResourcesCmd = &cobra.Command{
	Use:   "api_resources",
	Short: "Displays supported API resources",
	Run: func(cmd *cobra.Command, args []string) {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("Unable to find home directory: %v", err)
		}

		settingPath := filepath.Join(home, ".cfctl", "setting.toml")

		// Read main setting file
		mainV := viper.New()
		mainV.SetConfigFile(settingPath)
		mainV.SetConfigType("toml")
		mainConfigErr := mainV.ReadInConfig()

		var currentEnv string
		var envConfig map[string]interface{}

		if mainConfigErr == nil {
			currentEnv = mainV.GetString("environment")
			if currentEnv != "" {
				envConfig = mainV.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))
			}
		}

		if envConfig == nil {
			log.Fatalf("No configuration found for environment '%s'", currentEnv)
		}

		// Try to load endpoints from cache first
		endpointsMap, err := loadEndpointsFromCache(currentEnv)
		if err != nil {
			// If cache loading fails, fall back to fetching from identity service
			endpoint, ok := envConfig["endpoint"].(string)
			if !ok || endpoint == "" {
				log.Fatalf("No endpoint found for environment '%s'", currentEnv)
			}

			endpointsMap, err = FetchEndpointsMap(endpoint)
			if err != nil {
				log.Fatalf("Failed to fetch endpointsMap from '%s': %v", endpoint, err)
			}
		}

		// Load short names configuration
		shortNamesFile := filepath.Join(home, ".cfctl", "short_names.yaml")
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

func FetchEndpointsMap(endpoint string) (map[string]string, error) {
	// Parse the endpoint
	parts := strings.Split(endpoint, "://")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
	}

	scheme := parts[0]
	hostPort := parts[1]

	// Configure gRPC connection based on scheme
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

	// Establish the connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", endpoint, err)
	}
	defer conn.Close()

	// Use Reflection to discover services
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service and method
	serviceName := "spaceone.api.identity.v2.Endpoint"
	methodName := "list"

	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	methodDesc := serviceDesc.FindMethodByName(methodName)
	if methodDesc == nil {
		return nil, fmt.Errorf("method not found: %s", methodName)
	}

	// Dynamically create the request message
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

	// Set "query" field (optional)
	queryField := methodDesc.GetInputType().FindFieldByName("query")
	if queryField != nil && queryField.GetMessageType() != nil {
		queryMsg := dynamic.NewMessage(queryField.GetMessageType())
		// Set additional query fields here if needed
		reqMsg.SetFieldByName("query", queryMsg)
	}

	// Prepare an empty response message
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	// Full method name
	fullMethod := fmt.Sprintf("/%s/%s", serviceName, methodName)

	// Invoke the gRPC method
	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method %s: %v", fullMethod, err)
	}

	// Process the response to extract `service` and `endpoint`
	endpointsMap := make(map[string]string)
	resultsField := respMsg.FindFieldDescriptorByName("results")
	if resultsField == nil {
		return nil, fmt.Errorf("'results' field not found in response")
	}

	results := respMsg.GetField(resultsField).([]interface{})
	for _, result := range results {
		resultMsg := result.(*dynamic.Message)
		serviceName := resultMsg.GetFieldByName("service").(string)
		serviceEndpoint := resultMsg.GetFieldByName("endpoint").(string)
		endpointsMap[serviceName] = serviceEndpoint
	}

	return endpointsMap, nil
}

func callGRPCMethod(hostPort, service, method string, requestPayload interface{}) ([]byte, error) {
	// Configure gRPC connection
	var opts []grpc.DialOption
	if strings.HasPrefix(hostPort, "identity.api") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false, // Enable server certificate verification
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Connect to the gRPC server
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %v", err)
	}
	defer conn.Close()

	// Full method name (e.g., "/spaceone.api.identity.v2.Endpoint/List")
	fullMethod := fmt.Sprintf("/%s/%s", service, method)

	// Serialize the request payload to JSON
	reqData, err := json.Marshal(requestPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request payload: %v", err)
	}

	// Prepare a generic response container
	var respData json.RawMessage

	// Invoke the gRPC method
	err = conn.Invoke(context.Background(), fullMethod, reqData, &respData)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method: %v", err)
	}

	return respData, nil
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
	registeredShortNames, err := listShortNames()
    if err != nil {
        return nil, fmt.Errorf("failed to load short names: %v", err)
    }

    data := [][]string{}
    for _, s := range services {
        if strings.HasPrefix(s.Name, "grpc.reflection.v1alpha.") {
            continue
        }
        resourceName := s.Name[strings.LastIndex(s.Name, ".")+1:]
        verbs := getServiceMethods(client, s.Name)
        
        // Find matching short name
        var shortName string
        for sn, cmd := range registeredShortNames {
            if strings.Contains(cmd, fmt.Sprintf("%s list %s", service, resourceName)) {
                shortName = sn
                break
            }
        }
        
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

func renderTable(data [][]string) {
	// Calculate the dynamic width for the "Verb" column
	terminalWidth := pterm.GetTerminalWidth()
	usedWidth := 30 + 20 + 15 // Estimated widths for Service, Resource, and Short Names
	verbColumnWidth := terminalWidth - usedWidth
	if verbColumnWidth < 20 {
		verbColumnWidth = 20 // Minimum width for Verb column
	}

	// Use two distinct colors for alternating services
	alternateColors := []pterm.Color{
		pterm.FgLightBlue, pterm.FgLightYellow, pterm.FgLightMagenta, pterm.FgGreen, pterm.FgLightRed, pterm.FgBlue, pterm.FgLightGreen,
	}

	currentColorIndex := 0
	previousService := ""

	table := pterm.TableData{{"Service", "Verb", "Resource", "Short Names"}} // Column order updated

	for _, row := range data {
		service := row[0]

		// Switch color if the service name changes
		if service != previousService {
			currentColorIndex = (currentColorIndex + 1) % len(alternateColors)
			previousService = service
		}

		// Apply the current color
		color := alternateColors[currentColorIndex]
		coloredStyle := pterm.NewStyle(color)

		// Color the entire row (Service, Resource, Short Names, Verb)
		serviceColored := coloredStyle.Sprint(service)
		resourceColored := coloredStyle.Sprint(row[1])
		shortNamesColored := coloredStyle.Sprint(row[2])

		verbs := splitIntoLinesWithComma(row[3], verbColumnWidth)
		for i, line := range verbs {
			if i == 0 {
				table = append(table, []string{serviceColored, coloredStyle.Sprint(line), resourceColored, shortNamesColored})
			} else {
				table = append(table, []string{"", coloredStyle.Sprint(line), "", ""})
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
	ApiResourcesCmd.Flags().StringVarP(&endpoints, "service", "s", "", "Specify the services to connect to, separated by commas (e.g., 'identity', 'identity,inventory')")
}
