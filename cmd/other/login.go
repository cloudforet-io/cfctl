package other

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eiannone/keyboard"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jhump/protoreflect/dynamic"

	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/zalando/go-keyring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

//const encryptionKey = "spaceone-cfctl-encryption-key-32byte"

const (
	keyringService = "cfctl-credentials"
	keyringUser    = "encryption-key"
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
	// Load the environment-specific configuration without printing endpoint
	loadEnvironmentConfig()

	// Get current environment
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		exitWithError()
	}

	mainViper := viper.New()
	mainViper.SetConfigFile(filepath.Join(homeDir, ".cfctl", "config.yaml"))
	if err := mainViper.ReadInConfig(); err != nil {
		pterm.Error.Println("Failed to read main config file:", err)
		exitWithError()
	}

	currentEnv := mainViper.GetString("environment")
	if currentEnv == "" {
		pterm.Error.Println("No environment specified in config.yaml")
		exitWithError()
	}

	// Print endpoint once here
	pterm.Info.Printf("Using endpoint: %s\n", providedUrl)

	if strings.HasSuffix(currentEnv, "-app") {
		executeAppLogin(currentEnv, mainViper)
	} else {
		executeUserLogin(currentEnv)
	}
}

func executeAppLogin(currentEnv string, mainViper *viper.Viper) {
	token := mainViper.GetString(fmt.Sprintf("environments.%s.token", currentEnv))
	if token == "" {
		pterm.Error.Println("No App token found for app environment.")

		// Create a styled box for the app key type guidance
		headerBox := pterm.DefaultBox.WithTitle("App Guide").
			WithTitleTopCenter().
			WithRightPadding(4).
			WithLeftPadding(4).
			WithBoxStyle(pterm.NewStyle(pterm.FgLightCyan))

		appTokenExplain := "Please create a Domain Admin App in SpaceONE Console.\n" +
			"This requires Domain Admin privileges.\n\n" +
			"Or Please create a Workspace App in SpaceONE Console.\n" +
			"This requires Workspace Owner privileges."

		headerBox.Println(appTokenExplain)
		fmt.Println()

		// Create the steps content
		steps := []string{
			"1. Go to SpaceONE Console",
			"2. Navigate to either 'Admin > App Page' or specific 'Workspace > App page'",
			"3. Click 'Create' to create your App",
			"4. Copy value of either 'client_secret' from Client ID or 'token' from Spacectl (CLI)",
		}

		// Determine proxy value based on endpoint
		isIdentityEndpoint := strings.Contains(strings.ToLower(providedUrl), "identity")
		proxyValue := "true"
		if !isIdentityEndpoint {
			proxyValue = "false"
		}

		// Create yaml config example with highlighting
		yamlExample := pterm.DefaultBox.WithTitle("Config Example").
			WithTitleTopCenter().
			WithRightPadding(4).
			WithLeftPadding(4).
			Sprint(fmt.Sprintf("environment: %s\nenvironments:\n    %s:\n        endpoint: %s\n        proxy: %s\n        token: %s",
				currentEnv,
				currentEnv,
				providedUrl,
				proxyValue,
				pterm.FgLightCyan.Sprint("YOUR_COPIED_TOKEN")))

		// Create instruction box
		instructionBox := pterm.DefaultBox.WithTitle("Required Steps").
			WithTitleTopCenter().
			WithRightPadding(4).
			WithLeftPadding(4)

		// Combine all steps
		allSteps := append(steps,
			fmt.Sprintf("5. Add the token under the proxy in your config file:\n%s", yamlExample),
			"6. Run 'cfctl login' again")

		// Print all steps in the instruction box
		instructionBox.Println(strings.Join(allSteps, "\n\n"))

		exitWithError()
	}

	claims, ok := verifyAppToken(token)
	if !ok {
		exitWithError()
	}

	headerBox := pterm.DefaultBox.WithTitle("App Token Information").
		WithTitleTopCenter().
		WithRightPadding(4).
		WithLeftPadding(4).
		WithBoxStyle(pterm.NewStyle(pterm.FgLightCyan))

	var tokenInfo string
	roleType := claims["rol"].(string)

	if roleType == "DOMAIN_ADMIN" {
		tokenInfo = fmt.Sprintf("Role Type: %s\nDomain ID: %s\nAccess Scope: All Workspaces\nExpires: %s",
			pterm.FgGreen.Sprint("DOMAIN ADMIN"),
			claims["did"].(string),
			time.Unix(int64(claims["exp"].(float64)), 0).Format("2006-01-02 15:04:05"))
	} else if roleType == "WORKSPACE_OWNER" {
		tokenInfo = fmt.Sprintf("Role Type: %s\nDomain ID: %s\nWorkspace ID: %s\nExpires: %s",
			pterm.FgYellow.Sprint("WORKSPACE OWNER"),
			claims["did"].(string),
			claims["wid"].(string),
			time.Unix(int64(claims["exp"].(float64)), 0).Format("2006-01-02 15:04:05"))
	}

	headerBox.Println(tokenInfo)
	fmt.Println()

	pterm.Success.Println("Successfully authenticated with App token.")
}

func executeUserLogin(currentEnv string) {
	loadEnvironmentConfig()

	baseUrl := providedUrl
	if baseUrl == "" {
		pterm.Error.Println("No token endpoint specified in the configuration file.")
		exitWithError()
	}

	// Get the home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		exitWithError()
	}

	cacheViper := viper.New()
	cacheConfigPath := filepath.Join(homeDir, ".cfctl", "cache", "config.yaml")
	cacheViper.SetConfigFile(cacheConfigPath)

	var userID, password string
	if err := cacheViper.ReadInConfig(); err == nil {
		savedUserID := cacheViper.GetString(fmt.Sprintf("environments.%s.userID", currentEnv))
		savedEncryptedPassword := cacheViper.GetString(fmt.Sprintf("environments.%s.password", currentEnv))

		if savedUserID != "" && savedEncryptedPassword != "" {
			userID = savedUserID
			var err error
			password, err = decrypt(savedEncryptedPassword)
			if err != nil {
				pterm.Warning.Println("Failed to decrypt saved password, requesting new credentials")
				userID, password = promptCredentials()
			} else {
				pterm.Info.Printf("Using saved credentials for %s\n", userID)
			}
		} else {
			userID, password = promptCredentials()
		}
	} else {
		userID, password = promptCredentials()
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
	name := nameParts[1]

	// Fetch Domain ID using the base URL and domain name
	domainID, err := fetchDomainID(baseUrl, name)
	if err != nil {
		pterm.Error.Println("Failed to fetch Domain ID:", err)
		exitWithError()
	}

	// Issue tokens using user credentials
	accessToken, refreshToken, err := issueToken(baseUrl, userID, password, domainID)
	if err != nil {
		pterm.Error.Println("Failed to retrieve token:", err)
		exitWithError()
	}

	if encryptedPassword, err := encrypt(password); err == nil {
		saveCredentials(currentEnv, userID, encryptedPassword)
	} else {
		pterm.Warning.Printf("Failed to encrypt password: %v\n", err)
	}

	// Fetch workspaces
	workspaces, err := fetchWorkspaces(baseUrl, accessToken)
	if err != nil {
		pterm.Error.Println("Failed to fetch workspaces:", err)
		exitWithError()
	}

	// Fetch Domain ID and Role Type
	domainID, roleType, err := fetchDomainIDAndRole(baseUrl, accessToken)
	if err != nil {
		pterm.Error.Println("Failed to fetch Domain ID and Role Type:", err)
		exitWithError()
	}

	// Determine scope and workspace
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

	// Grant new token
	newAccessToken, err := grantToken(baseUrl, refreshToken, scope, domainID, workspaceID)
	if err != nil {
		pterm.Error.Println("Failed to retrieve new access token:", err)
		exitWithError()
	}

	// Save the new access token
	saveToken(newAccessToken)
	pterm.Success.Println("Successfully logged in and saved token.")
}

func getEncryptionKey() ([]byte, error) {
	key, err := keyring.Get(keyringService, keyringUser)
	if err == keyring.ErrNotFound {
		newKey := make([]byte, 32)
		if _, err := rand.Read(newKey); err != nil {
			return nil, fmt.Errorf("failed to generate new key: %v", err)
		}

		encodedKey := base64.StdEncoding.EncodeToString(newKey)
		if err := keyring.Set(keyringService, keyringUser, encodedKey); err != nil {
			return nil, fmt.Errorf("failed to store key in keychain: %v", err)
		}

		return newKey, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to access keychain: %v", err)
	}

	return base64.StdEncoding.DecodeString(key)
}

func encrypt(text string) (string, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("failed to get encryption key: %v", err)
	}

	plaintext := []byte(text)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	ciphertext := make([]byte, aes.BlockSize+len(plaintext))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}

	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], plaintext)

	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

func decrypt(cryptoText string) (string, error) {
	key, err := getEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("failed to get encryption key: %v", err)
	}

	ciphertext, err := base64.URLEncoding.DecodeString(cryptoText)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	if len(ciphertext) < aes.BlockSize {
		return "", errors.New("ciphertext too short")
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)

	return string(ciphertext), nil
}

func saveCredentials(currentEnv, userID, encryptedPassword string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Printf("Failed to get user home directory: %v\n", err)
		return
	}

	cacheDir := filepath.Join(homeDir, ".cfctl", "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		pterm.Error.Printf("Failed to create cache directory: %v\n", err)
		return
	}

	cacheConfigPath := filepath.Join(cacheDir, "config.yaml")

	if _, err := os.Stat(cacheConfigPath); os.IsNotExist(err) {
		if err := os.WriteFile(cacheConfigPath, []byte{}, 0600); err != nil {
			pterm.Error.Printf("Failed to create cache config file: %v\n", err)
			return
		}
	}

	cacheViper := viper.New()
	cacheViper.SetConfigFile(cacheConfigPath)

	if err := cacheViper.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		pterm.Error.Printf("Failed to read cache config: %v\n", err)
		return
	}

	if !cacheViper.IsSet("environments") {
		cacheViper.Set("environments", map[string]interface{}{})
	}

	envPath := fmt.Sprintf("environments.%s", currentEnv)
	envSettings := cacheViper.GetStringMap(envPath)
	if envSettings == nil {
		envSettings = make(map[string]interface{})
	}

	orderedSettings := make(map[string]interface{})

	if endpoint, exists := envSettings["endpoint"]; exists {
		orderedSettings["endpoint"] = endpoint
	} else {
		orderedSettings["endpoint"] = providedUrl
	}

	orderedSettings["proxy"] = true

	if token, exists := envSettings["token"]; exists {
		orderedSettings["token"] = token
	}

	orderedSettings["userid"] = userID

	orderedSettings["password"] = encryptedPassword

	cacheViper.Set(envPath, orderedSettings)

	if err := cacheViper.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to save credentials: %v\n", err)
		return
	}
}

func verifyAppToken(token string) (map[string]interface{}, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		pterm.Error.Println("Invalid token format")
		return nil, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		pterm.Error.Println("Failed to decode token payload:", err)
		return nil, false
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		pterm.Error.Println("Failed to parse token payload:", err)
		return nil, false
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		pterm.Error.Println("Expiration time not found in token")
		return nil, false
	}

	if time.Now().After(time.Unix(int64(exp), 0)) {
		pterm.DefaultBox.WithTitle("Expired App Token").
			WithTitleTopCenter().
			WithRightPadding(4).
			WithLeftPadding(4).
			WithBoxStyle(pterm.NewStyle(pterm.FgRed)).
			Println("Your App token has expired.\nPlease generate a new App and update your config file.")
		return nil, false
	}

	role, ok := claims["rol"].(string)
	if !ok {
		pterm.Error.Println("Role not found in token")
		return nil, false
	}

	if role != "DOMAIN_ADMIN" && role != "WORKSPACE_OWNER" {
		pterm.DefaultBox.WithTitle("Invalid App Token").
			WithTitleTopCenter().
			WithRightPadding(4).
			WithLeftPadding(4).
			WithBoxStyle(pterm.NewStyle(pterm.FgRed)).
			Println("App token must have either DOMAIN_ADMIN or WORKSPACE_OWNER role.\nPlease generate a new App with appropriate permissions and update your config file.")
		return nil, false
	}

	return claims, true
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
		pterm.Error.Printf("Failed to get user home directory: %v\n", err)
		exitWithError()
	}

	// Get current environment from main config
	mainViper := viper.New()
	mainConfigPath := filepath.Join(homeDir, ".cfctl", "config.yaml")
	mainViper.SetConfigFile(mainConfigPath)

	if err := mainViper.ReadInConfig(); err != nil {
		pterm.Error.Printf("Failed to read main config: %v\n", err)
		exitWithError()
	}

	currentEnvironment := mainViper.GetString("environment")
	if currentEnvironment == "" {
		pterm.Error.Printf("No environment specified in config\n")
		exitWithError()
	}

	// Determine which config file to use based on environment suffix
	var configPath string
	v := viper.New()

	if strings.HasSuffix(currentEnvironment, "-user") {
		// User configuration goes in cache directory
		cacheDir := filepath.Join(homeDir, ".cfctl", "cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create cache directory: %v\n", err)
			exitWithError()
		}
		configPath = filepath.Join(cacheDir, "config.yaml")
	} else if strings.HasSuffix(currentEnvironment, "-app") {
		// App configuration goes in main config
		configPath = mainConfigPath
	} else {
		pterm.Error.Printf("Invalid environment suffix (must end with -app or -user): %s\n", currentEnvironment)
		exitWithError()
	}

	// Initialize or read the config file
	v.SetConfigFile(configPath)

	// Create config file with basic structure if it doesn't exist
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		initialConfig := []byte("environments:\n")
		if err := os.WriteFile(configPath, initialConfig, 0644); err != nil {
			pterm.Error.Printf("Failed to create config file: %v\n", err)
			exitWithError()
		}
	}

	if err := v.ReadInConfig(); err != nil {
		pterm.Error.Printf("Failed to read config: %v\n", err)
		exitWithError()
	}

	// Get current environment settings
	envPath := fmt.Sprintf("environments.%s", currentEnvironment)
	envSettings := v.GetStringMap(envPath)
	if envSettings == nil {
		envSettings = make(map[string]interface{})
	}

	// Update token while preserving other settings
	envSettings["token"] = newToken

	// Save updated settings
	v.Set(envPath, envSettings)

	if err := v.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to save token: %v\n", err)
		exitWithError()
	}

	pterm.Success.Printf("Token successfully saved to %s\n", configPath)
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
	const pageSize = 9
	currentPage := 0
	searchTerm := ""
	filteredWorkspaces := workspaces

	// Initialize keyboard
	if err := keyboard.Open(); err != nil {
		pterm.Error.Println("Failed to initialize keyboard:", err)
		exitWithError()
	}
	defer keyboard.Close()

	for {
		// Apply search filter
		if searchTerm != "" {
			filteredWorkspaces = filterWorkspaces(workspaces, searchTerm)
		} else {
			filteredWorkspaces = workspaces
		}

		totalWorkspaces := len(filteredWorkspaces)
		totalPages := (totalWorkspaces + pageSize - 1) / pageSize

		startIndex := currentPage * pageSize
		endIndex := startIndex + pageSize
		if endIndex > totalWorkspaces {
			endIndex = totalWorkspaces
		}

		// Clear screen
		fmt.Print("\033[H\033[2J")

		// Show search term if active
		if searchTerm != "" {
			pterm.Info.Printf("Search term: %s\n", searchTerm)
		}

		pterm.Info.Printf("Available Options (Page %d of %d):\n", currentPage+1, totalPages)

		// Always show DOMAIN ADMIN option on first page
		if currentPage == 0 {
			pterm.DefaultBasicText.WithStyle(pterm.NewStyle(pterm.FgLightCyan)).
				Printf("  0: DOMAIN ADMIN\n")
		}

		// Display current page items
		for i := startIndex; i < endIndex; i++ {
			name := filteredWorkspaces[i]["name"].(string)
			fmt.Printf("  %d: %s\n", i+1, name)
		}

		// Show navigation help
		fmt.Print("\nNavigation: [p]revious page, [n]ext page")
		if searchTerm != "" {
			fmt.Print(", [c]lear search")
		}
		fmt.Print(", [/]search, [q]uit\n")
		fmt.Print("> ")

		// Get keyboard input
		char, _, err := keyboard.GetKey()
		if err != nil {
			pterm.Error.Println("Error reading keyboard input:", err)
			exitWithError()
		}

		switch char {
		case 'n', 'N':
			if currentPage < totalPages-1 {
				currentPage++
			} else {
				currentPage = 0
			}
		case 'p', 'P':
			if currentPage > 0 {
				currentPage--
			} else {
				currentPage = totalPages - 1
			}
		case 'q', 'Q':
			pterm.Error.Println("Workspace selection cancelled.")
			os.Exit(1)
		case 'c', 'C':
			searchTerm = ""
			currentPage = 0
		case '/':
			keyboard.Close()
			fmt.Print("\nEnter search term: ")
			var input string
			fmt.Scanln(&input)
			searchTerm = input
			currentPage = 0
			keyboard.Open()
		case '0':
			return "0"
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			index := int(char - '0')
			adjustedIndex := startIndex + (index - 1)
			if adjustedIndex >= 0 && adjustedIndex < len(filteredWorkspaces) {
				return filteredWorkspaces[adjustedIndex]["workspace_id"].(string)
			}
		}
	}
}

func filterWorkspaces(workspaces []map[string]interface{}, searchTerm string) []map[string]interface{} {
	var filtered []map[string]interface{}
	searchTerm = strings.ToLower(searchTerm)

	for _, workspace := range workspaces {
		name := strings.ToLower(workspace["name"].(string))
		if strings.Contains(name, searchTerm) {
			filtered = append(filtered, workspace)
		}
	}
	return filtered
}

func init() {
	LoginCmd.Flags().StringVarP(&providedUrl, "url", "u", "", "The URL to use for login (e.g. cfctl login -u https://example.com)")
}
