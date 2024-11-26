package other

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/jhump/protoreflect/dynamic"

	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/types/known/structpb"
)

var providedUrl string

// LoginCmd represents the login command
var LoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to SpaceONE",
	Long: `A command that allows you to login to SpaceONE.
It will prompt you for your User ID, Password, and fetch the Domain ID automatically, then fetch the token.`,
	Run: executeLogin,
}

// tokenAuth implements grpc.PerRPCCredentials for token-based authentication.
type tokenAuth struct {
	token string
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"token": t.token, // "Authorization: Bearer" 대신 "token" 키 사용
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return true
}

func executeLogin(cmd *cobra.Command, args []string) {
	// Load the environment-specific configuration
	loadEnvironmentConfig()

	// Set baseUrl directly from providedUrl loaded in loadEnvironmentConfig
	baseUrl := providedUrl
	if baseUrl == "" {
		pterm.Error.Println("No token endpoint specified in the configuration file.")
		exitWithError()
	}

	token := viper.GetString("token")
	if token != "" && !isTokenExpired(token) {
		pterm.Info.Println("Existing token found and it is still valid. Attempting to authenticate with saved credentials.")
		if verifyToken(token) {
			pterm.Success.Println("Successfully authenticated with saved token.")
			return
		}
		pterm.Warning.Println("Saved token is invalid, proceeding with login.")
	}

	userID, password := promptCredentials()

	// Get the home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		exitWithError()
	}

	// Load the main config file specifically for environment name
	mainViper := viper.New()
	mainViper.SetConfigFile(filepath.Join(homeDir, ".cfctl", "config.yaml"))
	if err := mainViper.ReadInConfig(); err != nil {
		pterm.Error.Println("Failed to read main config file:", err)
		exitWithError()
	}

	// Extract the middle part of the environment name for `name`
	currentEnvironment := mainViper.GetString("environment")
	nameParts := strings.Split(currentEnvironment, "-")
	if len(nameParts) < 3 {
		pterm.Error.Println("Environment name format is invalid.")
		exitWithError()
	}
	name := nameParts[1] // Extract the middle part, e.g., "cloudone" from "dev-cloudone-user"

	// Fetch Domain ID using the base URL and domain name
	domainID, err := fetchDomainID(baseUrl, name)
	if err != nil {
		pterm.Error.Println("Failed to fetch Domain ID:", err)
		exitWithError()
	}

	// Issue tokens (access token and refresh token) using user credentials
	accessToken, refreshToken, err := issueToken(baseUrl, userID, password, domainID)
	if err != nil {
		pterm.Error.Println("Failed to retrieve token:", err)
		exitWithError()
	}

	// Fetch workspaces available to the user
	workspaces, err := fetchWorkspaces(baseUrl, accessToken)
	if err != nil {
		pterm.Error.Println("Failed to fetch workspaces:", err)
		exitWithError()
	}

	// Fetch Domain ID and Role Type using the access token
	domainID, roleType, err := fetchDomainIDAndRole(baseUrl, accessToken)
	if err != nil {
		pterm.Error.Println("Failed to fetch Domain ID and Role Type:", err)
		exitWithError()
	}

	// Determine the appropriate scope and workspace ID based on the role type
	scope := determineScope(roleType, len(workspaces))
	var workspaceID string
	if roleType == "DOMAIN_ADMIN" {
		workspaceID = selectScopeOrWorkspace(workspaces)
		if workspaceID == "0" {
			scope = "DOMAIN"
			workspaceID = ""
		} else {
			scope = "WORKSPACE"
		}
	} else {
		workspaceID = selectWorkspace(workspaces)
		scope = "WORKSPACE"
	}

	// Grant a new access token using the refresh token and selected scope
	newAccessToken, err := grantToken(baseUrl, refreshToken, scope, domainID, workspaceID)
	if err != nil {
		pterm.Error.Println("Failed to retrieve new access token:", err)
		exitWithError()
	}

	// Save the new access token
	saveToken(newAccessToken)
	pterm.Success.Println("Successfully logged in and saved token.")
}

// Load environment-specific configuration based on the selected environment
func loadEnvironmentConfig() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		exitWithError()
	}

	mainConfigPath := filepath.Join(homeDir, ".cfctl", "config.yaml")
	cacheConfigPath := filepath.Join(homeDir, ".cfctl", "cache", "config.yaml")

	viper.SetConfigFile(mainConfigPath)
	if err := viper.ReadInConfig(); err != nil {
		pterm.Error.Println("Failed to read config.yaml:", err)
		exitWithError()
	}

	currentEnvironment := viper.GetString("environment")
	if currentEnvironment == "" {
		pterm.Error.Println("No environment specified in config.yaml")
		exitWithError()
	}

	configFound := false
	for _, configPath := range []string{mainConfigPath, cacheConfigPath} {
		v := viper.New()
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err == nil {
			endpointKey := fmt.Sprintf("environments.%s.endpoint", currentEnvironment)
			tokenKey := fmt.Sprintf("environments.%s.token", currentEnvironment)

			if providedUrl == "" {
				providedUrl = v.GetString(endpointKey)
			}

			if token := v.GetString(tokenKey); token != "" {
				viper.Set("token", token)
			}

			if providedUrl != "" {
				configFound = true
			}
		}
	}

	if !configFound {
		pterm.Error.Printf("No endpoint found for the current environment '%s'\n", currentEnvironment)
		exitWithError()
	}

	isProxyEnabled := viper.GetBool(fmt.Sprintf("environments.%s.proxy", currentEnvironment))
	containsIdentity := strings.Contains(strings.ToLower(providedUrl), "identity")

	if !isProxyEnabled && !containsIdentity {
		pterm.DefaultBox.WithTitle("Proxy Mode Required").
			WithTitleTopCenter().
			WithBoxStyle(pterm.NewStyle(pterm.FgYellow)).
			Println("Current endpoint is not configured for identity service.\n" +
				"Please enable proxy mode and set identity endpoint first.")

		pterm.DefaultBox.WithBoxStyle(pterm.NewStyle(pterm.FgCyan)).
			Println("$ cfctl config endpoint -s identity\n" +
				"$ cfctl login")

		exitWithError()
	}

	pterm.Info.Printf("Using endpoint: %s\n", providedUrl)
}

func determineScope(roleType string, workspaceCount int) string {
	switch roleType {
	case "DOMAIN_ADMIN":
		return "DOMAIN"
	case "USER":
		return "WORKSPACE"
	default:
		pterm.Error.Println("Unknown role_type:", roleType)
		exitWithError()
		return "" // Unreachable
	}
}

func isTokenExpired(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		pterm.Error.Println("Invalid token format.")
		return true
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		pterm.Error.Println("Failed to decode token payload:", err)
		return true
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		pterm.Error.Println("Failed to unmarshal token payload:", err)
		return true
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		pterm.Error.Println("Expiration time (exp) not found in token.")
		return true
	}

	expirationTime := time.Unix(int64(exp), 0)
	return time.Now().After(expirationTime)
}

func verifyToken(token string) bool {
	// This function should implement token verification logic, for example by making a request to an endpoint that requires authentication
	// Returning true for simplicity in this example
	return true
}

func exitWithError() {
	os.Exit(1)
}

func promptCredentials() (string, string) {
	userId, _ := pterm.DefaultInteractiveTextInput.Show("Enter your user ID")
	passwordInput := pterm.DefaultInteractiveTextInput.WithMask("*")
	password, _ := passwordInput.Show("Enter your password")
	return userId, password
}

func fetchDomainID(baseUrl string, name string) (string, error) {
	// Parse the endpoint
	parts := strings.Split(baseUrl, "://")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid endpoint format: %s", baseUrl)
	}

	hostPort := parts[1]

	// Configure gRPC connection
	var opts []grpc.DialOption
	if strings.HasPrefix(baseUrl, "grpc+ssl://") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Establish connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Create reflection client
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service
	serviceName := "spaceone.api.identity.v2.Domain"
	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	// Find the method descriptor
	methodDesc := serviceDesc.FindMethodByName("get_auth_info")
	if methodDesc == nil {
		return "", fmt.Errorf("method get_auth_info not found")
	}

	// Create request message
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	reqMsg.SetFieldByName("name", name)

	// Make the gRPC call
	fullMethod := fmt.Sprintf("/%s/%s", serviceName, "get_auth_info")
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return "", fmt.Errorf("RPC failed: %v", err)
	}

	// Extract domain_id from response
	domainID, err := respMsg.TryGetFieldByName("domain_id")
	if err != nil {
		return "", fmt.Errorf("failed to get domain_id from response: %v", err)
	}

	return domainID.(string), nil
}

func issueToken(baseUrl, userID, password, domainID string) (string, string, error) {
	// Parse the endpoint
	parts := strings.Split(baseUrl, "://")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid endpoint format: %s", baseUrl)
	}

	hostPort := parts[1]

	// Configure gRPC connection
	var opts []grpc.DialOption
	if strings.HasPrefix(baseUrl, "grpc+ssl://") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Establish connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return "", "", fmt.Errorf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Create reflection client
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service
	serviceName := "spaceone.api.identity.v2.Token"
	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	// Find the method descriptor
	methodDesc := serviceDesc.FindMethodByName("issue")
	if methodDesc == nil {
		return "", "", fmt.Errorf("method issue not found")
	}

	// Create request message
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

	// Create credentials struct using protobuf types
	structpb := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"user_id": {
				Kind: &structpb.Value_StringValue{
					StringValue: userID,
				},
			},
			"password": {
				Kind: &structpb.Value_StringValue{
					StringValue: password,
				},
			},
		},
	}

	// Set all fields in the request message
	reqMsg.SetFieldByName("credentials", structpb)
	reqMsg.SetFieldByName("auth_type", int32(1)) // LOCAL = 1
	reqMsg.SetFieldByName("timeout", int32(0))
	reqMsg.SetFieldByName("verify_code", "")
	reqMsg.SetFieldByName("domain_id", domainID)

	// Make the gRPC call
	fullMethod := fmt.Sprintf("/%s/%s", serviceName, "issue")
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return "", "", fmt.Errorf("RPC failed: %v", err)
	}

	// Extract tokens from response
	accessToken, err := respMsg.TryGetFieldByName("access_token")
	if err != nil {
		return "", "", fmt.Errorf("failed to get access_token from response: %v", err)
	}

	refreshToken, err := respMsg.TryGetFieldByName("refresh_token")
	if err != nil {
		return "", "", fmt.Errorf("failed to get refresh_token from response: %v", err)
	}

	return accessToken.(string), refreshToken.(string), nil
}

func fetchWorkspaces(baseUrl string, accessToken string) ([]map[string]interface{}, error) {
	// Parse the endpoint
	parts := strings.Split(baseUrl, "://")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint format: %s", baseUrl)
	}

	hostPort := parts[1]

	// Configure gRPC connection
	var opts []grpc.DialOption
	if strings.HasPrefix(baseUrl, "grpc+ssl://") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Add token credentials
	creds := &tokenAuth{
		token: accessToken,
	}
	opts = append(opts, grpc.WithPerRPCCredentials(creds))

	// Establish connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Create reflection client
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service
	serviceName := "spaceone.api.identity.v2.UserProfile"
	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	// Find the method descriptor
	methodDesc := serviceDesc.FindMethodByName("get_workspaces")
	if methodDesc == nil {
		return nil, fmt.Errorf("method get_workspaces not found")
	}

	// Create request message
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

	// Create metadata with token
	md := metadata.New(map[string]string{
		"token": accessToken,
	})
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	// Make the gRPC call
	fullMethod := "/spaceone.api.identity.v2.UserProfile/get_workspaces"
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	err = conn.Invoke(ctx, fullMethod, reqMsg, respMsg)
	if err != nil {
		return nil, fmt.Errorf("RPC failed: %v", err)
	}

	// Extract results from response
	results, err := respMsg.TryGetFieldByName("results")
	if err != nil {
		return nil, fmt.Errorf("failed to get results from response: %v", err)
	}

	workspaces, ok := results.([]interface{})
	if !ok || len(workspaces) == 0 {
		pterm.Warning.Println("There are no accessible workspaces. Ask your administrators or workspace owners for access.")
		exitWithError()
	}

	var workspaceList []map[string]interface{}
	for _, workspace := range workspaces {
		workspaceMsg, ok := workspace.(*dynamic.Message)
		if !ok {
			return nil, fmt.Errorf("failed to parse workspace message")
		}

		workspaceMap := make(map[string]interface{})
		fields := workspaceMsg.GetKnownFields()

		for _, field := range fields {
			if value, err := workspaceMsg.TryGetFieldByName(field.GetName()); err == nil {
				workspaceMap[field.GetName()] = value
			}
		}

		workspaceList = append(workspaceList, workspaceMap)
	}

	return workspaceList, nil
}

func fetchDomainIDAndRole(baseUrl string, accessToken string) (string, string, error) {
	// Parse the endpoint
	parts := strings.Split(baseUrl, "://")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid endpoint format: %s", baseUrl)
	}

	hostPort := parts[1]

	// Configure gRPC connection
	var opts []grpc.DialOption
	if strings.HasPrefix(baseUrl, "grpc+ssl://") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Add token to metadata
	opts = append(opts, grpc.WithPerRPCCredentials(&tokenAuth{token: accessToken}))

	// Establish connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return "", "", fmt.Errorf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Create reflection client
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service
	serviceName := "spaceone.api.identity.v2.UserProfile"
	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	// Find the method descriptor
	methodDesc := serviceDesc.FindMethodByName("get")
	if methodDesc == nil {
		return "", "", fmt.Errorf("method get not found")
	}

	// Create request message
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

	// Make the gRPC call
	fullMethod := fmt.Sprintf("/%s/%s", serviceName, "get")
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return "", "", fmt.Errorf("RPC failed: %v", err)
	}

	// Extract domain_id and role_type from response
	domainID, err := respMsg.TryGetFieldByName("domain_id")
	if err != nil {
		return "", "", fmt.Errorf("failed to get domain_id from response: %v", err)
	}

	roleType, err := respMsg.TryGetFieldByName("role_type")
	if err != nil {
		return "", "", fmt.Errorf("failed to get role_type from response: %v", err)
	}

	// Convert roleType to string based on enum value
	var roleTypeStr string
	switch v := roleType.(type) {
	case int32:
		switch v {
		case 1:
			roleTypeStr = "DOMAIN_ADMIN"
		case 2:
			roleTypeStr = "WORKSPACE_OWNER"
		case 3:
			roleTypeStr = "WORKSPACE_MEMBER"
		default:
			return "", "", fmt.Errorf("unknown role_type: %d", v)
		}
	case string:
		roleTypeStr = v
	default:
		return "", "", fmt.Errorf("unexpected role_type type: %T", roleType)
	}

	return domainID.(string), roleTypeStr, nil
}

func grantToken(baseUrl, refreshToken, scope, domainID, workspaceID string) (string, error) {
	// Parse the endpoint
	parts := strings.Split(baseUrl, "://")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid endpoint format: %s", baseUrl)
	}

	hostPort := parts[1]

	// Configure gRPC connection
	var opts []grpc.DialOption
	if strings.HasPrefix(baseUrl, "grpc+ssl://") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Establish connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %v", err)
	}
	defer conn.Close()

	// Create reflection client
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service
	serviceName := "spaceone.api.identity.v2.Token"
	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	// Find the method descriptor
	methodDesc := serviceDesc.FindMethodByName("grant")
	if methodDesc == nil {
		return "", fmt.Errorf("method grant not found")
	}

	// Create request message
	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

	reqMsg.SetFieldByName("grant_type", int32(1))

	var scopeEnum int32
	switch scope {
	case "DOMAIN":
		scopeEnum = 2
	case "WORKSPACE":
		scopeEnum = 3
	case "USER":
		scopeEnum = 5
	default:
		return "", fmt.Errorf("unknown scope: %s", scope)
	}

	reqMsg.SetFieldByName("scope", scopeEnum)
	reqMsg.SetFieldByName("token", refreshToken)
	reqMsg.SetFieldByName("timeout", int32(86400))
	reqMsg.SetFieldByName("domain_id", domainID)
	if workspaceID != "" {
		reqMsg.SetFieldByName("workspace_id", workspaceID)
	}

	// Make the gRPC call
	fullMethod := "/spaceone.api.identity.v2.Token/grant"
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return "", fmt.Errorf("RPC failed: %v", err)
	}

	// Extract access_token from response
	accessToken, err := respMsg.TryGetFieldByName("access_token")
	if err != nil {
		return "", fmt.Errorf("failed to get access_token from response: %v", err)
	}

	return accessToken.(string), nil
}

// saveToken updates the token in the appropriate configuration file based on the environment suffix
func saveToken(newToken string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		exitWithError()
	}

	// Load the main environment file to get the current environment
	viper.SetConfigFile(filepath.Join(homeDir, ".cfctl", "config.yaml"))
	if err := viper.ReadInConfig(); err != nil {
		pterm.Error.Println("Failed to read environment file:", err)
		exitWithError()
	}

	currentEnvironment := viper.GetString("environment")
	if currentEnvironment == "" {
		pterm.Error.Println("No environment specified in environment.yaml")
		exitWithError()
	}

	// Determine the appropriate config file based on environment suffix
	var configPath string
	if strings.HasSuffix(currentEnvironment, "-user") {
		configPath = filepath.Join(homeDir, ".cfctl", "cache", "config.yaml")
	} else {
		configPath = filepath.Join(homeDir, ".cfctl", "config.yaml")
	}

	// Read the target config file
	content, err := ioutil.ReadFile(configPath)
	if err != nil {
		pterm.Error.Println("Failed to read configuration file:", err)
		exitWithError()
	}

	lines := strings.Split(string(content), "\n")
	var newContent []string
	var envContent []string
	inTargetEnv := false
	indentLevel := 0

	for i, line := range lines {
		trimmedLine := strings.TrimRight(line, " \t")

		if strings.HasPrefix(strings.TrimSpace(trimmedLine), "environments:") {
			indentLevel = strings.Index(trimmedLine, "environments:")
			newContent = append(newContent, trimmedLine)
			continue
		}

		if strings.HasPrefix(strings.TrimSpace(trimmedLine), currentEnvironment+":") {
			inTargetEnv = true
			newContent = append(newContent, trimmedLine)
			continue
		}

		if inTargetEnv {
			if len(trimmedLine) > 0 && !strings.HasPrefix(trimmedLine, strings.Repeat(" ", indentLevel+2)) {
				sortedEnvContent := sortEnvironmentContent(envContent, newToken, indentLevel+4)
				newContent = append(newContent, sortedEnvContent...)
				envContent = nil
				inTargetEnv = false
				newContent = append(newContent, trimmedLine)
			} else if !strings.HasPrefix(strings.TrimSpace(trimmedLine), "token:") && trimmedLine != "" {
				envContent = append(envContent, trimmedLine)
				continue
			}
		} else {
			newContent = append(newContent, trimmedLine)
		}

		if inTargetEnv && i == len(lines)-1 {
			sortedEnvContent := sortEnvironmentContent(envContent, newToken, indentLevel+4)
			newContent = append(newContent, sortedEnvContent...)
		}
	}

	// Write the modified content back to the file
	err = ioutil.WriteFile(configPath, []byte(strings.Join(newContent, "\n")+"\n"), 0644)
	if err != nil {
		pterm.Error.Println("Failed to save updated token to configuration file:", err)
		exitWithError()
	}

	pterm.Success.Println("Token successfully saved to", configPath)
}

// sortEnvironmentContent sorts the environment content to ensure token is at the end
func sortEnvironmentContent(content []string, token string, indentLevel int) []string {
	var sorted []string
	var endpointLine, proxyLine string

	for _, line := range content {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "endpoint:") {
			endpointLine = line
		} else if strings.HasPrefix(trimmed, "proxy:") {
			proxyLine = line
		}
	}

	if endpointLine != "" {
		sorted = append(sorted, endpointLine)
	}
	if proxyLine != "" {
		sorted = append(sorted, proxyLine)
	}

	sorted = append(sorted, strings.Repeat(" ", indentLevel)+"token: "+token)

	return sorted
}

func selectWorkspace(workspaces []map[string]interface{}) string {
	const pageSize = 15
	totalWorkspaces := len(workspaces)
	totalPages := (totalWorkspaces + pageSize - 1) / pageSize

	currentPage := 0
	for {
		startIndex := currentPage * pageSize
		endIndex := startIndex + pageSize
		if endIndex > totalWorkspaces {
			endIndex = totalWorkspaces
		}

		var options []string
		for i := startIndex; i < endIndex; i++ {
			name := workspaces[i]["name"].(string)
			options = append(options, fmt.Sprintf("%d: %s", i+1, name))
		}

		if currentPage > 0 {
			options = append([]string{"< Previous Page"}, options...)
		}
		if endIndex < totalWorkspaces {
			options = append(options, "Next Page >")
		}

		pterm.Info.Printfln("Available Workspaces (Page %d of %d):", currentPage+1, totalPages)
		selectedOption, err := pterm.DefaultInteractiveSelect.
			WithOptions(options).
			WithMaxHeight(20).
			Show()
		if err != nil {
			pterm.Error.Println("Error selecting workspace:", err)
			exitWithError()
		}

		if selectedOption == "< Previous Page" {
			currentPage--
			continue
		} else if selectedOption == "Next Page >" {
			currentPage++
			continue
		}

		var index int
		fmt.Sscanf(selectedOption, "%d", &index)

		if index >= 1 && index <= totalWorkspaces {
			return workspaces[index-1]["workspace_id"].(string)
		} else {
			pterm.Error.Println("Invalid selection. Please try again.")
		}
	}
}

func selectScopeOrWorkspace(workspaces []map[string]interface{}) string {
	const pageSize = 15
	totalWorkspaces := len(workspaces)
	totalPages := (totalWorkspaces + pageSize - 1) / pageSize

	currentPage := 0
	for {
		startIndex := currentPage * pageSize
		endIndex := startIndex + pageSize
		if endIndex > totalWorkspaces {
			endIndex = totalWorkspaces
		}

		var options []string
		if currentPage == 0 {
			options = append(options, "0: DOMAIN ADMIN")
		}
		for i := startIndex; i < endIndex; i++ {
			name := workspaces[i]["name"].(string)
			options = append(options, fmt.Sprintf("%d: %s", i+1, name))
		}

		if currentPage > 0 {
			options = append([]string{"< Previous Page"}, options...)
		}
		if endIndex < totalWorkspaces {
			options = append(options, "Next Page >")
		}

		pterm.Info.Printfln("Available Options (Page %d of %d):", currentPage+1, totalPages)
		selectedOption, err := pterm.DefaultInteractiveSelect.
			WithOptions(options).
			WithMaxHeight(20).
			Show()
		if err != nil {
			pterm.Error.Println("Error selecting option:", err)
			exitWithError()
		}

		if selectedOption == "< Previous Page" {
			currentPage--
			continue
		} else if selectedOption == "Next Page >" {
			currentPage++
			continue
		} else if selectedOption == "0: DOMAIN ADMIN" {
			return "0"
		}

		var index int
		fmt.Sscanf(selectedOption, "%d", &index)

		if index >= 1 && index <= totalWorkspaces {
			return workspaces[index-1]["workspace_id"].(string)
		} else {
			pterm.Error.Println("Invalid selection. Please try again.")
		}
	}
}

func init() {
	LoginCmd.Flags().StringVarP(&providedUrl, "url", "u", "", "The URL to use for login (e.g. cfctl login -u https://example.com)")
	//LoginCmd.MarkFlagRequired("url")
}
