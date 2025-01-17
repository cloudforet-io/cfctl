package configs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

// GetAPIEndpoint fetches the actual API endpoint from the config endpoint
func GetAPIEndpoint(endpoint string) (string, error) {
	// Handle gRPC+SSL protocol
	if strings.HasPrefix(endpoint, "grpc+ssl://") || strings.HasPrefix(endpoint, "grpc://") {
		// For gRPC+SSL endpoints, return as is since it's already in the correct format
		return endpoint, nil
	}

	// Remove protocol prefix if exists
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	// Construct config endpoint
	configURL := fmt.Sprintf("https://%s/config/production.json", endpoint)

	// Make HTTP request
	resp, err := http.Get(configURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch config, status code: %d", resp.StatusCode)
	}

	// Parse JSON response
	var config struct {
		ConsoleAPIV2 struct {
			Endpoint string `json:"ENDPOINT"`
		} `json:"CONSOLE_API_V2"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return "", fmt.Errorf("failed to parse config: %v", err)
	}

	if config.ConsoleAPIV2.Endpoint == "" {
		return "", fmt.Errorf("no API endpoint found in config")
	}

	return strings.TrimSuffix(config.ConsoleAPIV2.Endpoint, "/"), nil
}

// GetIdentityEndpoint fetches the identity service endpoint from the API endpoint
func GetIdentityEndpoint(apiEndpoint string) (string, bool, error) {
	// If the endpoint is already gRPC+SSL
	if strings.HasPrefix(apiEndpoint, "grpc+ssl://") || strings.HasPrefix(apiEndpoint, "grpc://") {
		// Check if it contains 'identity'
		containsIdentity := strings.Contains(apiEndpoint, "identity")

		// Remove /v1 suffix if present
		if idx := strings.Index(apiEndpoint, "/v"); idx != -1 {
			apiEndpoint = apiEndpoint[:idx]
		}

		return apiEndpoint, containsIdentity, nil
	}

	// Original HTTP/HTTPS handling logic
	endpointListURL := fmt.Sprintf("%s/identity/endpoint/list", apiEndpoint)

	payload := map[string]string{}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", false, fmt.Errorf("failed to create payload: %v", err)
	}

	req, err := http.NewRequest("POST", endpointListURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", false, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("failed to fetch endpoints: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Service  string `json:"service"`
			Endpoint string `json:"endpoint"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, fmt.Errorf("failed to parse response: %v", err)
	}

	for _, service := range result.Results {
		if service.Service == "identity" {
			endpoint := service.Endpoint
			if idx := strings.Index(endpoint, "/v"); idx != -1 {
				endpoint = endpoint[:idx]
			}
			return endpoint, true, nil
		}
	}

	return "", false, nil
}

func GetServiceEndpoint(config *Environments, serviceName string) (string, error) {
	envConfig := config.Environments[config.Environment]
	if envConfig.Endpoint == "" {
		return "", fmt.Errorf("endpoint not found in environment config")
	}

	if strings.HasPrefix(envConfig.Endpoint, "grpc://") {
		// Allow both localhost and cluster-internal addresses
		if strings.Contains(envConfig.Endpoint, "localhost") || strings.Contains(envConfig.Endpoint, ".svc.cluster.local") {
			return envConfig.Endpoint, nil
		}
	}

	// Get console API endpoint
	apiEndpoint, err := GetAPIEndpoint(envConfig.Endpoint)
	if err != nil {
		pterm.Error.Printf("Failed to get API endpoint: %v\n", err)
		os.Exit(1)
	}

	// Get identity endpoint
	identityEndpoint, _, err := GetIdentityEndpoint(apiEndpoint)

	// Fetch endpoints map
	endpointsMap, err := FetchEndpointsMap(identityEndpoint)
	if err != nil {
		return "", fmt.Errorf("failed to fetch endpoints map: %v", err)
	}

	// Get endpoint for the requested service
	endpoint, exists := endpointsMap[serviceName]
	if !exists {
		return "", fmt.Errorf("no endpoint found for service: %s", serviceName)
	}

	// Remove /v1 suffix if present
	if idx := strings.Index(endpoint, "/v"); idx != -1 {
		endpoint = endpoint[:idx]
	}

	return endpoint, nil
}

func FetchEndpointsMap(endpoint string) (map[string]string, error) {
	if strings.HasPrefix(endpoint, "grpc://localhost") {
		endpointsMap := make(map[string]string)
		endpointsMap["static"] = endpoint
		return endpointsMap, nil
	}

	// Get identity service endpoint
	identityEndpoint, hasIdentityService, err := GetIdentityEndpoint(endpoint)
	listEndpointsUrl := endpoint + "/identity/endpoint/list"

	if err != nil {
		pterm.Error.Printf("Failed to get identity endpoint: %v\n", err)
		os.Exit(1)
	}

	if !hasIdentityService {
		// Handle gRPC+SSL protocol directly
		if strings.HasPrefix(endpoint, "grpc+ssl://") || strings.HasPrefix(endpoint, "grpc://") {
			protocol := "grpc+ssl://"
			if strings.HasPrefix(endpoint, "grpc://") {
				protocol = "grpc://"
			}

			// Parse the endpoint
			hostPart := strings.TrimPrefix(endpoint, protocol)
			hostPart = strings.TrimSuffix(hostPart, "/")

			hostParts := strings.Split(hostPart, ".")
			if len(hostParts) == 0 {
				return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
			}

			svc := hostParts[0]
			baseDomain := strings.Join(hostParts[1:], ".")

			// Configure TLS
			tlsConfig := &tls.Config{
				InsecureSkipVerify: false,
			}
			opts := []grpc.DialOption{
				grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
			}

			//If current service is not identity, modify hostPort to use identity service
			if svc != "identity" {
				hostPort := fmt.Sprintf("identity.%s", baseDomain)
				endpoints, err := invokeGRPCEndpointList(hostPort, opts)
				if err != nil {
					return nil, fmt.Errorf("failed to get endpoints from gRPC: %v", err)
				}
				return endpoints, nil
			}
		}

		payload := map[string]string{}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequest("POST", listEndpointsUrl, bytes.NewBuffer(jsonPayload))
		if err != nil {
			return nil, err
		}

		req.Header.Set("accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch endpoints, status code: %d", resp.StatusCode)
		}

		var result struct {
			Results []struct {
				Service  string `json:"service"`
				Endpoint string `json:"endpoint"`
			} `json:"results"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		// Convert to endpointsMap format
		endpointsMap := make(map[string]string)
		for _, service := range result.Results {
			endpointsMap[service.Service] = service.Endpoint
		}

		return endpointsMap, nil
	} else {
		// Parse the endpoint
		parts := strings.Split(identityEndpoint, "://")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid endpoint format: %s", identityEndpoint)
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
			return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", identityEndpoint, err)
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
}

func invokeGRPCEndpointList(hostPort string, opts []grpc.DialOption) (map[string]string, error) {
	// Wrap the entire operation in a function that can recover from panic
	var endpoints = make(map[string]string)
	var err error

	defer func() {
		if r := recover(); r != nil {
			switch x := r.(type) {
			case string:
				err = fmt.Errorf("error: %s", x)
			case error:
				err = x
			default:
				err = fmt.Errorf("unknown panic: %v", r)
			}
		}
	}()

	// Establish the connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", hostPort, err)
	}
	defer conn.Close()

	// Use Reflection to discover services
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

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

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())
	fullMethod := fmt.Sprintf("/%s/%s", serviceName, methodName)

	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method %s: %v", fullMethod, err)
	}

	resultsField := respMsg.FindFieldDescriptorByName("results")
	if resultsField == nil {
		return nil, fmt.Errorf("'results' field not found in response")
	}

	results := respMsg.GetField(resultsField).([]interface{})
	for _, result := range results {
		resultMsg := result.(*dynamic.Message)
		serviceName := resultMsg.GetFieldByName("service").(string)
		serviceEndpoint := resultMsg.GetFieldByName("endpoint").(string)
		endpoints[serviceName] = serviceEndpoint
	}

	return endpoints, nil
}
