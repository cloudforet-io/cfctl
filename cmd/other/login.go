package other

import (
	"bytes"
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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
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
		"token": t.token, // Use "token" key instead of "Authorization: Bearer"
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return true
}

func executeLogin(cmd *cobra.Command, args []string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		return
	}

	configPath := filepath.Join(homeDir, ".cfctl", "setting.yaml")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		pterm.Error.Println("No valid configuration found.")
		pterm.Info.Println("Please run 'cfctl setting init' to set up your configuration.")
		pterm.Info.Println("After initialization, run 'cfctl login' to authenticate.")
		return
	}

	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")
	if err := viper.ReadInConfig(); err != nil {
		pterm.Error.Printf("Failed to read config file: %v\n", err)
		return
	}

	currentEnv := viper.GetString("environment")
	if currentEnv == "" {
		pterm.Error.Println("No environment selected")
		return
	}

	// Check if it's an app environment
	if strings.HasSuffix(currentEnv, "-app") {
		pterm.DefaultBox.WithTitle("App Environment Detected").
			WithTitleTopCenter().
			WithRightPadding(4).
			WithLeftPadding(4).
			WithBoxStyle(pterm.NewStyle(pterm.FgYellow)).
			Println("Login command is not available for app environments.\nPlease use the app token directly in your configuration file.")
		return
	}

	// Execute normal user login
	executeUserLogin(currentEnv)
}

type TokenInfo struct {
	Token string `yaml:"token"`
}

// promptToken prompts for token input
func promptToken() (string, error) {
	prompt := &survey.Password{
		Message: "Enter your token:",
	}

	var token string
	err := survey.AskOne(prompt, &token, survey.WithValidator(survey.Required))
	if err != nil {
		return "", err
	}

	return token, nil
}

// saveAppToken saves the token
func saveAppToken(currentEnv, token string) error {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".cfctl", "config.yaml")

	viper.SetConfigFile(configPath)
	if err := viper.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		return err
	}

	envPath := fmt.Sprintf("environments.%s", currentEnv)
	envSettings := viper.GetStringMap(envPath)
	if envSettings == nil {
		envSettings = make(map[string]interface{})
	}

	// Initialize tokens array if it doesn't exist
	var tokens []TokenInfo
	if existingTokens, ok := envSettings["tokens"]; ok {
		if tokenList, ok := existingTokens.([]interface{}); ok {
			for _, t := range tokenList {
				if tokenMap, ok := t.(map[string]interface{}); ok {
					tokenInfo := TokenInfo{
						Token: tokenMap["token"].(string),
					}
					tokens = append(tokens, tokenInfo)
				}
			}
		}
	}

	// Add new token if it doesn't exist
	tokenExists := false
	for _, t := range tokens {
		if t.Token == token {
			tokenExists = true
			break
		}
	}

	if !tokenExists {
		newToken := TokenInfo{
			Token: token,
		}
		tokens = append(tokens, newToken)
	}

	// Update environment settings
	envSettings["tokens"] = tokens

	// Keep the existing endpoint and proxy settings
	if endpoint, ok := envSettings["endpoint"]; ok {
		envSettings["endpoint"] = endpoint
	}
	if proxy, ok := envSettings["proxy"]; ok {
		envSettings["proxy"] = proxy
	}

	viper.Set(envPath, envSettings)
	return viper.WriteConfig()
}

// promptTokenSelection shows available tokens and lets user select one
func promptTokenSelection(tokens []TokenInfo) (string, error) {
	if len(tokens) == 0 {
		return "", fmt.Errorf("no tokens available")
	}

	if err := keyboard.Open(); err != nil {
		return "", err
	}
	defer keyboard.Close()

	selectedIndex := 0
	for {
		fmt.Print("\033[H\033[2J") // Clear screen

		pterm.DefaultHeader.WithFullWidth().
			WithBackgroundStyle(pterm.NewStyle(pterm.BgDarkGray)).
			WithTextStyle(pterm.NewStyle(pterm.FgLightWhite)).
			Println("Select a token:")

		// Display available tokens
		for i, token := range tokens {
			maskedToken := maskToken(token.Token)
			if i == selectedIndex {
				pterm.Printf("→ %d: %s\n", i+1, maskedToken)
			} else {
				pterm.Printf("  %d: %s\n", i+1, maskedToken)
			}
		}

		pterm.DefaultBasicText.WithStyle(pterm.NewStyle(pterm.FgGray)).
			Println("\nNavigation: [j]down [k]up [Enter]select [q]quit")

		char, key, err := keyboard.GetKey()
		if err != nil {
			return "", err
		}

		switch key {
		case keyboard.KeyEnter:
			return tokens[selectedIndex].Token, nil
		}

		switch char {
		case 'j':
			if selectedIndex < len(tokens)-1 {
				selectedIndex++
			}
		case 'k':
			if selectedIndex > 0 {
				selectedIndex--
			}
		case 'q', 'Q':
			return "", fmt.Errorf("selection cancelled")
		}
	}
}

// maskToken returns a masked version of the token for display
func maskToken(token string) string {
	if len(token) <= 10 {
		return strings.Repeat("*", len(token))
	}
	return token[:5] + "..." + token[len(token)-5:]
}

// executeAppLogin handles login for app environments
func executeAppLogin(currentEnv string) error {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".cfctl", "config.yaml")

	viper.SetConfigFile(configPath)
	if err := viper.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		return err
	}

	envPath := fmt.Sprintf("environments.%s.tokens", currentEnv)
	var tokens []TokenInfo
	if tokensList := viper.Get(envPath); tokensList != nil {
		if tokenList, ok := tokensList.([]interface{}); ok {
			for _, t := range tokenList {
				if tokenMap, ok := t.(map[string]interface{}); ok {
					tokenInfo := TokenInfo{
						Token: tokenMap["token"].(string),
					}
					tokens = append(tokens, tokenInfo)
				}
			}
		}
	}

	if err := keyboard.Open(); err != nil {
		return err
	}
	defer keyboard.Close()

	selectedIndex := 0
	options := []string{"Enter a new token"}
	var validTokens []TokenInfo // New slice to store only valid tokens

	for _, tokenInfo := range tokens {
		claims, err := validateAndDecodeToken(tokenInfo.Token)
		if err != nil {
			pterm.Warning.Printf("Invalid token found in config: %v\n", err)
			continue
		}

		displayName := getTokenDisplayName(claims)
		options = append(options, displayName)
		validTokens = append(validTokens, tokenInfo)
	}

	if len(validTokens) == 0 && len(tokens) > 0 {
		pterm.Warning.Println("All existing tokens are invalid. Please enter a new token.")
		// Clear invalid tokens from config
		if err := clearInvalidTokens(currentEnv); err != nil {
			pterm.Warning.Printf("Failed to clear invalid tokens: %v\n", err)
		}
	}

	for {
		fmt.Print("\033[H\033[2J") // Clear screen

		pterm.DefaultHeader.WithFullWidth().
			WithBackgroundStyle(pterm.NewStyle(pterm.BgDarkGray)).
			WithTextStyle(pterm.NewStyle(pterm.FgLightWhite)).
			Println("Choose an option:")

		for i, option := range options {
			if i == selectedIndex {
				pterm.Printf("→ %d: %s\n", i, option)
			} else {
				pterm.Printf("  %d: %s\n", i, option)
			}
		}

		pterm.DefaultBasicText.WithStyle(pterm.NewStyle(pterm.FgGray)).
			Println("\nNavigation: [j]down [k]up [Enter]select [q]uit")

		char, key, err := keyboard.GetKey()
		if err != nil {
			return err
		}

		switch key {
		case keyboard.KeyEnter:
			if selectedIndex == 0 {
				// Enter a new token
				token, err := promptToken()
				if err != nil {
					return err
				}

				// Validate new token before saving
				if _, err := validateAndDecodeToken(token); err != nil {
					return fmt.Errorf("invalid token: %v", err)
				}

				// First save to tokens array
				if err := saveAppToken(currentEnv, token); err != nil {
					return err
				}
				// Then set as current token
				if err := saveSelectedToken(currentEnv, token); err != nil {
					return err
				}
				pterm.Success.Printf("Token successfully saved and selected\n")
				return nil
			} else {
				// Use selected token from existing valid tokens
				selectedToken := validTokens[selectedIndex-1].Token
				if err := saveSelectedToken(currentEnv, selectedToken); err != nil {
					return fmt.Errorf("failed to save selected token: %v", err)
				}
				pterm.Success.Printf("Token successfully selected\n")
				return nil
			}
		}

		switch char {
		case 'j':
			if selectedIndex < len(options)-1 {
				selectedIndex++
			}
		case 'k':
			if selectedIndex > 0 {
				selectedIndex--
			}
		case 'q', 'Q':
			pterm.Error.Println("Selection cancelled.")
			os.Exit(1)
		}
	}
}

func getTokenDisplayName(claims map[string]interface{}) string {
	role := claims["rol"].(string)
	domainID := claims["did"].(string)

	if role == "WORKSPACE_OWNER" {
		workspaceID := claims["wid"].(string)
		return fmt.Sprintf("%s (%s, %s)", role, domainID, workspaceID)
	}

	return fmt.Sprintf("%s (%s)", role, domainID)
}

func executeUserLogin(currentEnv string) {
	loadEnvironmentConfig()

	baseUrl := providedUrl
	if baseUrl == "" {
		pterm.Error.Println("No token endpoint specified in the configuration file.")
		exitWithError()
	}

	homeDir, _ := os.UserHomeDir()
	mainViper := viper.New()
	settingPath := filepath.Join(homeDir, ".cfctl", "setting.yaml")
	mainViper.SetConfigFile(settingPath)
	mainViper.SetConfigType("yaml")

	if err := mainViper.ReadInConfig(); err != nil {
		pterm.Error.Printf("Failed to read config file: %v\n", err)
		exitWithError()
	}

	// Get console API endpoint
	apiEndpoint, err := GetAPIEndpoint(baseUrl)
	if err != nil {
		pterm.Error.Printf("Failed to get API endpoint: %v\n", err)
		exitWithError()
	}
	restIdentityEndpoint := apiEndpoint + "/identity"

	// Get identity service endpoint
	identityEndpoint, hasIdentityService, err := GetIdentityEndpoint(apiEndpoint)
	if err != nil {
		pterm.Error.Printf("Failed to get identity endpoint: %v\n", err)
		exitWithError()
	}

	var scope string
	if !hasIdentityService {
		client := &http.Client{}

		// Check for existing user_id in config
		userID := mainViper.GetString(fmt.Sprintf("environments.%s.user_id", currentEnv))
		var tempUserID string
		if userID == "" {
			userIDInput := pterm.DefaultInteractiveTextInput
			tempUserID, _ = userIDInput.Show("Enter your User ID")
		} else {
			tempUserID = userID
			pterm.Info.Printf("Logging in as: %s\n", userID)
		}

		var accessToken, refreshToken string
		existingAccessToken, existingRefreshToken, err := getValidTokens(currentEnv)
		if err == nil && existingRefreshToken != "" && !isTokenExpired(existingRefreshToken) {
			accessToken = existingAccessToken
			refreshToken = existingRefreshToken
		} else {
			passwordInput := pterm.DefaultInteractiveTextInput.WithMask("*")
			password, _ := passwordInput.Show("Enter your password")

			endpoint := mainViper.GetString(fmt.Sprintf("environments.%s.endpoint", currentEnv))
			if endpoint == "" {
				pterm.Error.Println("endpoint not found in configuration")
				exitWithError()
			}

			endpoint = strings.TrimPrefix(endpoint, "https://")
			endpoint = strings.TrimPrefix(endpoint, "http://")

			parts := strings.Split(endpoint, ".")
			if len(parts) < 3 {
				pterm.Error.Printf("Invalid endpoint format: %s\n", endpoint)
				exitWithError()
			}
			domainName := parts[0]

			domainPayload := map[string]string{"name": domainName}
			jsonPayload, _ := json.Marshal(domainPayload)

			req, err := http.NewRequest("POST", restIdentityEndpoint+"/domain/get-auth-info", bytes.NewBuffer(jsonPayload))
			if err != nil {
				pterm.Error.Printf("Failed to create request: %v\n", err)
				exitWithError()
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				pterm.Error.Printf("Failed to fetch domain info: %v\n", err)
				exitWithError()
			}
			defer resp.Body.Close()

			var result map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				pterm.Error.Printf("Failed to decode response: %v\n", err)
				exitWithError()
			}

			domainID, ok := result["domain_id"].(string)
			if !ok {
				pterm.Error.Println("Domain ID not found in response")
				exitWithError()
			}

			tokenPayload := map[string]interface{}{
				"credentials": map[string]string{
					"user_id":  tempUserID,
					"password": password,
				},
				"auth_type": "LOCAL",
				"domain_id": domainID,
			}

			jsonPayload, _ = json.Marshal(tokenPayload)
			req, _ = http.NewRequest("POST", restIdentityEndpoint+"/token/issue", bytes.NewBuffer(jsonPayload))
			req.Header.Set("Content-Type", "application/json")

			resp, err = client.Do(req)
			if err != nil {
				pterm.Error.Printf("Failed to issue token: %v\n", err)
				exitWithError()
			}
			defer resp.Body.Close()

			var tokenResult map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&tokenResult); err != nil {
				pterm.Error.Printf("Failed to decode token response: %v\n", err)
				exitWithError()
			}

			accessToken, ok = tokenResult["access_token"].(string)
			if !ok {
				pterm.Error.Println("Access token not found in response")
				exitWithError()
			}

			refreshToken, ok = tokenResult["refresh_token"].(string)
			if !ok {
				pterm.Error.Println("Refresh token not found in response")
				exitWithError()
			}
		}

		if userID == "" {
			mainViper.Set(fmt.Sprintf("environments.%s.user_id", currentEnv), tempUserID)
			if err := mainViper.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to save user ID to config: %v\n", err)
				exitWithError()
			}
		}

		// Extract domain name from environment
		nameParts := strings.Split(currentEnv, "-")
		if len(nameParts) < 2 {
			pterm.Error.Println("Environment name format is invalid.")
			exitWithError()
		}

		// Create cache directory and save tokens
		envCacheDir := filepath.Join(homeDir, ".cfctl", "cache", currentEnv)
		if err := os.MkdirAll(envCacheDir, 0700); err != nil {
			pterm.Error.Printf("Failed to create cache directory: %v\n", err)
			exitWithError()
		}

		pterm.Info.Printf("Logged in as %s\n", tempUserID)

		// Use the tokens to fetch workspaces and role
		workspaces, err := fetchWorkspaces(restIdentityEndpoint, identityEndpoint, hasIdentityService, accessToken)
		if err != nil {
			pterm.Error.Println("Failed to fetch workspaces:", err)
			exitWithError()
		}

		domainID, roleType, err := fetchDomainIDAndRole(restIdentityEndpoint, identityEndpoint, hasIdentityService, accessToken)
		if err != nil {
			pterm.Error.Println("Failed to fetch Domain ID and Role Type:", err)
			exitWithError()
		}

		// Determine scope and select workspace
		scope = determineScope(roleType, len(workspaces))
		var workspaceID string
		if roleType == "DOMAIN_ADMIN" {
			workspaceID = selectScopeOrWorkspace(workspaces, roleType)
			if workspaceID == "0" {
				scope = "DOMAIN"
				workspaceID = ""
			} else {
				scope = "WORKSPACE"
			}
		} else {
			workspaceID = selectWorkspaceOnly(workspaces)
			scope = "WORKSPACE"
		}

		// Grant new token using the refresh token
		newAccessToken, err := grantToken(restIdentityEndpoint, identityEndpoint, hasIdentityService, refreshToken, scope, domainID, workspaceID)
		if err != nil {
			pterm.Error.Println("Failed to retrieve new access token:", err)
			exitWithError()
		}

		// Save all tokens
		if err := os.WriteFile(filepath.Join(envCacheDir, "refresh_token"), []byte(refreshToken), 0600); err != nil {
			pterm.Error.Printf("Failed to save refresh token: %v\n", err)
			exitWithError()
		}

		if err := os.WriteFile(filepath.Join(envCacheDir, "access_token"), []byte(newAccessToken), 0600); err != nil {
			pterm.Error.Printf("Failed to save access token: %v\n", err)
			exitWithError()
		}

		pterm.Success.Println("Successfully logged in and saved token.")
		return
	} else {
		// Extract domain name from environment
		nameParts := strings.Split(currentEnv, "-")
		if len(nameParts) < 2 {
			pterm.Error.Println("Environment name format is invalid.")
			exitWithError()
		}
		name := nameParts[0]

		// Check for existing user_id in config
		userID := mainViper.GetString(fmt.Sprintf("environments.%s.user_id", currentEnv))
		var tempUserID string

		if userID == "" {
			userIDInput := pterm.DefaultInteractiveTextInput
			tempUserID, _ = userIDInput.Show("Enter your User ID")
		} else {
			tempUserID = userID
			pterm.Info.Printf("Logging in as: %s\n", userID)
		}

		// Fetch Domain ID
		domainID, err := fetchDomainID(identityEndpoint, name)
		if err != nil {
			pterm.Error.Println("Failed to fetch Domain ID:", err)
			exitWithError()
		}

		accessToken, refreshToken, err := getValidTokens(currentEnv)
		if err != nil || refreshToken == "" || isTokenExpired(refreshToken) {
			// Get new tokens with password
			password := promptPassword()
			accessToken, refreshToken, err = issueToken(identityEndpoint, tempUserID, password, domainID)
			if err != nil {
				pterm.Error.Printf("Failed to issue token: %v\n", err)
				exitWithError()
			}

			// Only save user_id after successful token issue
			if userID == "" {
				mainViper.Set(fmt.Sprintf("environments.%s.user_id", currentEnv), tempUserID)
				if err := mainViper.WriteConfig(); err != nil {
					pterm.Error.Printf("Failed to save user ID to config: %v\n", err)
					exitWithError()
				}
			}
		}

		// Use the tokens to fetch workspaces and role
		workspaces, err := fetchWorkspaces(restIdentityEndpoint, identityEndpoint, hasIdentityService, accessToken)
		if err != nil {
			pterm.Error.Println("Failed to fetch workspaces:", err)
			exitWithError()
		}

		domainID, roleType, err := fetchDomainIDAndRole(restIdentityEndpoint, identityEndpoint, hasIdentityService, accessToken)
		if err != nil {
			pterm.Error.Println("Failed to fetch Domain ID and Role Type:", err)
			exitWithError()
		}

		// Determine scope and select workspace
		scope = determineScope(roleType, len(workspaces))
		var workspaceID string
		if roleType == "DOMAIN_ADMIN" {
			workspaceID = selectScopeOrWorkspace(workspaces, roleType)
			if workspaceID == "0" {
				scope = "DOMAIN"
				workspaceID = ""
			} else {
				scope = "WORKSPACE"
			}
		} else {
			workspaceID = selectWorkspaceOnly(workspaces)
			scope = "WORKSPACE"
		}

		// Grant new token using the refresh token
		newAccessToken, err := grantToken("", identityEndpoint, hasIdentityService, refreshToken, scope, domainID, workspaceID)
		if err != nil {
			pterm.Error.Println("Failed to retrieve new access token:", err)
			exitWithError()
		}

		// Create cache directory
		envCacheDir := filepath.Join(homeDir, ".cfctl", "cache", currentEnv)
		if err := os.MkdirAll(envCacheDir, 0700); err != nil {
			pterm.Error.Printf("Failed to create cache directory: %v\n", err)
			exitWithError()
		}

		// Save tokens
		if err := os.WriteFile(filepath.Join(envCacheDir, "refresh_token"), []byte(refreshToken), 0600); err != nil {
			pterm.Error.Printf("Failed to save refresh token: %v\n", err)
			exitWithError()
		}

		if err := os.WriteFile(filepath.Join(envCacheDir, "access_token"), []byte(newAccessToken), 0600); err != nil {
			pterm.Error.Printf("Failed to save access token: %v\n", err)
			exitWithError()
		}

		pterm.Success.Println("Successfully logged in and saved token.")
	}
}

// GetAPIEndpoint fetches the actual API endpoint from the config endpoint
func GetAPIEndpoint(endpoint string) (string, error) {
	// Handle gRPC+SSL protocol
	if strings.HasPrefix(endpoint, "grpc+ssl://") {
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
	if strings.HasPrefix(apiEndpoint, "grpc+ssl://") {
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

// Prompt for password when token is expired
func promptPassword() string {
	passwordInput := pterm.DefaultInteractiveTextInput.WithMask("*")
	password, _ := passwordInput.Show("Enter your password")
	return password
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

// Define a struct for user credentials
type UserCredentials struct {
	UserID   string `yaml:"userid"`
	Password string `yaml:"password"`
	Token    string `yaml:"token"`
}

// saveCredentials saves the user's credentials to the configuration
func saveCredentials(currentEnv, userID, encryptedPassword, accessToken, refreshToken, grantToken string) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get home directory:", err)
		exitWithError()
	}

	// Update main settings file
	settingPath := filepath.Join(homeDir, ".cfctl", "setting.yaml")
	mainViper := viper.New()
	mainViper.SetConfigFile(settingPath)
	mainViper.SetConfigType("yaml")

	if err := mainViper.ReadInConfig(); err != nil {
		pterm.Error.Printf("Failed to read config file: %v\n", err)
		exitWithError()
	}

	// Save user_id to environment settings
	envPath := fmt.Sprintf("environments.%s.user_id", currentEnv)
	mainViper.Set(envPath, userID)

	if err := mainViper.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to save config file: %v\n", err)
		exitWithError()
	}

	// Create cache directory
	envCacheDir := filepath.Join(homeDir, ".cfctl", "cache", currentEnv)
	if err := os.MkdirAll(envCacheDir, 0700); err != nil {
		pterm.Error.Printf("Failed to create cache directory: %v\n", err)
		exitWithError()
	}

	// Save tokens to cache
	if err := os.WriteFile(filepath.Join(envCacheDir, "access_token"), []byte(accessToken), 0600); err != nil {
		pterm.Error.Printf("Failed to save access token: %v\n", err)
		exitWithError()
	}

	if refreshToken != "" {
		if err := os.WriteFile(filepath.Join(envCacheDir, "refresh_token"), []byte(refreshToken), 0600); err != nil {
			pterm.Error.Printf("Failed to save refresh token: %v\n", err)
			exitWithError()
		}
	}

	if grantToken != "" {
		if err := os.WriteFile(filepath.Join(envCacheDir, "grant_token"), []byte(grantToken), 0600); err != nil {
			pterm.Error.Printf("Failed to save grant token: %v\n", err)
			exitWithError()
		}
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

	settingPath := filepath.Join(homeDir, ".cfctl", "setting.yaml")
	viper.SetConfigFile(settingPath)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		pterm.Error.Printf("Failed to read setting file: %v\n", err)
		exitWithError()
	}

	currentEnv := viper.GetString("environment")
	if currentEnv == "" {
		pterm.Error.Println("No environment selected")
		exitWithError()
	}

	v := viper.New()
	v.SetConfigFile(settingPath)
	if err := v.ReadInConfig(); err == nil {
		endpointKey := fmt.Sprintf("environments.%s.endpoint", currentEnv)
		tokenKey := fmt.Sprintf("environments.%s.token", currentEnv)

		if providedUrl == "" {
			providedUrl = v.GetString(endpointKey)
		}

		if token := v.GetString(tokenKey); token != "" {
			viper.Set("token", token)
		}
	}

	isProxyEnabled := viper.GetBool(fmt.Sprintf("environments.%s.proxy", currentEnv))
	containsIdentity := strings.Contains(strings.ToLower(providedUrl), "identity")

	if !isProxyEnabled && !containsIdentity {
		pterm.DefaultBox.WithTitle("Proxy Mode Required").
			WithTitleTopCenter().
			WithBoxStyle(pterm.NewStyle(pterm.FgYellow)).
			Println("Current endpoint is not configured for identity service.\n" +
				"Please enable proxy mode and set identity endpoint first.")

		pterm.DefaultBox.WithBoxStyle(pterm.NewStyle(pterm.FgCyan)).
			Println("$ cfctl setting endpoint -s identity\n" +
				"$ cfctl login")

		exitWithError()
	}
}

func determineScope(roleType string, workspaceCount int) string {
	switch roleType {
	case "DOMAIN_ADMIN":
		return "DOMAIN"
	case "WORKSPACE_OWNER", "WORKSPACE_MEMBER", "USER":
		return "WORKSPACE"
	default:
		pterm.Error.Println("Unknown role_type:", roleType)
		exitWithError()
		return "" // Unreachable
	}
}

// isTokenExpired checks if the token is expired
func isTokenExpired(token string) bool {
	claims, err := decodeJWT(token)
	if err != nil {
		return true // 디코딩 실패 시 만료된 것으로 간주
	}

	if exp, ok := claims["exp"].(float64); ok {
		return time.Now().Unix() > int64(exp)
	}
	return true
}

func verifyToken(token string) bool {
	// This function should implement token verification logic, for example by making a request to an endpoint that requires authentication
	// Returning true for simplicity in this example
	return true
}

func exitWithError() {
	os.Exit(1)
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

func fetchWorkspaces(baseUrl string, identityEndpoint string, hasIdentityService bool, accessToken string) ([]map[string]interface{}, error) {
	if !hasIdentityService {
		payload := map[string]string{}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}

		getWorkspacesUrl := baseUrl + "/user-profile/get-workspaces"
		req, err := http.NewRequest("POST", getWorkspacesUrl, bytes.NewBuffer(jsonPayload))
		if err != nil {
			return nil, err
		}

		req.Header.Set("accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to fetch workspaces, status code: %d", resp.StatusCode)
		}

		var result map[string]interface{}
		if err := json.Unmarshal(responseBody, &result); err != nil {
			return nil, err
		}

		workspaces, ok := result["results"].([]interface{})
		if !ok || len(workspaces) == 0 {
			pterm.Warning.Println("There are no accessible workspaces. Ask your administrators or workspace owners for access.")
			exitWithError()
		}

		var workspaceList []map[string]interface{}
		for _, workspace := range workspaces {
			workspaceMap, ok := workspace.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("failed to parse workspace data")
			}
			workspaceList = append(workspaceList, workspaceMap)
		}

		return workspaceList, nil
	} else {
		// Parse the endpoint
		parts := strings.Split(identityEndpoint, "://")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid endpoint format: %s", identityEndpoint)
		}

		hostPort := parts[1]

		// Configure gRPC connection
		var opts []grpc.DialOption
		if strings.HasPrefix(identityEndpoint, "grpc+ssl://") {
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
}

func fetchDomainIDAndRole(baseUrl string, identityEndpoint string, hasIdentityService bool, accessToken string) (string, string, error) {
	if !hasIdentityService {
		payload := map[string]string{}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			return "", "", err
		}

		getUserProfileUrl := baseUrl + "/user-profile/get"
		req, err := http.NewRequest("POST", getUserProfileUrl, bytes.NewBuffer(jsonPayload))
		if err != nil {
			return "", "", err
		}

		req.Header.Set("accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return "", "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", "", fmt.Errorf("failed to fetch user profile, status code: %d", resp.StatusCode)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", "", err
		}

		domainID, ok := result["domain_id"].(string)
		if !ok {
			return "", "", fmt.Errorf("domain_id not found in response")
		}

		roleType, ok := result["role_type"].(string)
		if !ok {
			return "", "", fmt.Errorf("role_type not found in response")
		}

		return domainID, roleType, nil
	} else {
		// Parse the endpoint
		parts := strings.Split(identityEndpoint, "://")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid endpoint format: %s", identityEndpoint)
		}

		hostPort := parts[1]

		// Configure gRPC connection
		var opts []grpc.DialOption
		if strings.HasPrefix(identityEndpoint, "grpc+ssl://") {
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
}

func grantToken(restIdentityEndpoint, identityEndpoint string, hasIdentityService bool, refreshToken, scope, domainID, workspaceID string) (string, error) {
	if !hasIdentityService {
		payload := map[string]interface{}{
			"grant_type":   "REFRESH_TOKEN",
			"token":        refreshToken,
			"scope":        scope,
			"timeout":      10800,
			"domain_id":    domainID,
			"workspace_id": workspaceID,
		}
		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}

		req, err := http.NewRequest("POST", restIdentityEndpoint+"/token/grant", bytes.NewBuffer(jsonPayload))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("status code: %d", resp.StatusCode)
		}

		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", err
		}

		accessToken, ok := result["access_token"].(string)
		if !ok {
			return "", fmt.Errorf("access token not found in response")
		}

		return accessToken, nil
	} else {
		// Parse the endpoint
		parts := strings.Split(identityEndpoint, "://")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid endpoint format: %s", identityEndpoint)
		}

		hostPort := parts[1]

		// Configure gRPC connection
		var opts []grpc.DialOption
		if strings.HasPrefix(identityEndpoint, "grpc+ssl://") {
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
		reqMsg.SetFieldByName("timeout", int32(10800))
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
}

// saveSelectedToken saves the selected token as the current token for the environment
func saveSelectedToken(currentEnv, selectedToken string) error {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".cfctl", "config.yaml")

	viper.SetConfigFile(configPath)
	if err := viper.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		return err
	}

	envPath := fmt.Sprintf("environments.%s", currentEnv)
	envSettings := viper.GetStringMap(envPath)
	if envSettings == nil {
		envSettings = make(map[string]interface{})
	}

	// Keep all existing settings
	newEnvSettings := make(map[string]interface{})

	// Keep endpoint and proxy settings
	if endpoint, ok := envSettings["endpoint"]; ok {
		newEnvSettings["endpoint"] = endpoint
	}
	if proxy, ok := envSettings["proxy"]; ok {
		newEnvSettings["proxy"] = proxy
	}

	// Keep tokens array
	if tokens, ok := envSettings["tokens"]; ok {
		newEnvSettings["tokens"] = tokens
	}

	// Set the selected token as current token
	newEnvSettings["token"] = selectedToken

	viper.Set(envPath, newEnvSettings)
	return viper.WriteConfig()
}

func selectScopeOrWorkspace(workspaces []map[string]interface{}, roleType string) string {
	if err := keyboard.Open(); err != nil {
		pterm.Error.Println("Failed to initialize keyboard:", err)
		exitWithError()
	}
	defer keyboard.Close()

	if roleType != "DOMAIN_ADMIN" {
		return selectWorkspaceOnly(workspaces)
	}

	options := []string{"DOMAIN ADMIN", "WORKSPACES"}
	selectedIndex := 0

	for {
		fmt.Print("\033[H\033[2J")

		// Display scope selection
		pterm.DefaultHeader.WithFullWidth().
			WithBackgroundStyle(pterm.NewStyle(pterm.BgDarkGray)).
			WithTextStyle(pterm.NewStyle(pterm.FgLightWhite)).
			Println("Select Scope")

		for i, option := range options {
			if i == selectedIndex {
				pterm.Printf("→ %d: %s\n", i, option)
			} else {
				pterm.Printf("  %d: %s\n", i, option)
			}
		}

		// Show navigation help
		pterm.DefaultBasicText.WithStyle(pterm.NewStyle(pterm.FgGray)).
			Println("\nNavigation: [j]down [k]up, [Enter]select, [q]uit")

		// Get keyboard input
		char, key, err := keyboard.GetKey()
		if err != nil {
			pterm.Error.Println("Error reading keyboard input:", err)
			exitWithError()
		}

		// Handle navigation and other commands
		switch key {
		case keyboard.KeyEnter:
			if selectedIndex == 0 {
				return "0"
			} else {
				return selectWorkspaceOnly(workspaces)
			}
		}

		switch char {
		case 'j': // Down
			if selectedIndex < len(options)-1 {
				selectedIndex++
			}
		case 'k': // Up
			if selectedIndex > 0 {
				selectedIndex--
			}
		case 'q', 'Q':
			pterm.Error.Println("Selection cancelled.")
			os.Exit(1)
		}
	}
}

// selectWorkspaceOnly handles workspace selection
func selectWorkspaceOnly(workspaces []map[string]interface{}) string {
	const pageSize = 15
	currentPage := 0
	searchMode := false
	searchTerm := ""
	selectedIndex := 0
	inputBuffer := ""
	filteredWorkspaces := workspaces

	if err := keyboard.Open(); err != nil {
		pterm.Error.Println("Failed to initialize keyboard:", err)
		exitWithError()
	}
	defer keyboard.Close()

	for {
		// Clear screen
		fmt.Print("\033[H\033[2J")

		// Apply search filter
		if searchTerm != "" {
			filteredWorkspaces = filterWorkspaces(workspaces, searchTerm)
			if len(filteredWorkspaces) == 0 {
				filteredWorkspaces = workspaces
			}
		} else {
			filteredWorkspaces = workspaces
		}

		// Calculate pagination
		totalWorkspaces := len(filteredWorkspaces)
		totalPages := (totalWorkspaces + pageSize - 1) / pageSize
		startIndex := (currentPage % totalPages) * pageSize
		endIndex := startIndex + pageSize
		if endIndex > totalWorkspaces {
			endIndex = totalWorkspaces
		}

		// Display header with page information
		pterm.DefaultHeader.WithFullWidth().
			WithBackgroundStyle(pterm.NewStyle(pterm.BgDarkGray)).
			WithTextStyle(pterm.NewStyle(pterm.FgLightWhite)).
			Printf("Accessible Workspaces (Page %d of %d)", currentPage+1, totalPages)

		// Show workspace list
		for i := startIndex; i < endIndex; i++ {
			name := filteredWorkspaces[i]["name"].(string)
			if i-startIndex == selectedIndex {
				pterm.Printf("→ %d: %s\n", i+1, name)
			} else {
				pterm.Printf("  %d: %s\n", i+1, name)
			}
		}

		// Show navigation help and search prompt
		pterm.DefaultBasicText.WithStyle(pterm.NewStyle(pterm.FgGray)).
			Println("\nNavigation: [h]prev-page [j]down [k]up  [l]next-page [/]search [q]uit")

		// Show search or input prompt at the bottom
		if searchMode {
			fmt.Println()
			pterm.Info.Printf("Search (ESC to cancel, Enter to confirm): %s", searchTerm)
		} else {
			fmt.Print("\nSelect a workspace above or input a number: ")
			if inputBuffer != "" {
				fmt.Print(inputBuffer)
			}
		}

		// Get keyboard input
		char, key, err := keyboard.GetKey()
		if err != nil {
			pterm.Error.Println("Error reading keyboard input:", err)
			exitWithError()
		}

		// Handle search mode input
		if searchMode {
			switch key {
			case keyboard.KeyEsc:
				searchMode = false
				searchTerm = ""
			case keyboard.KeyBackspace, keyboard.KeyBackspace2:
				if len(searchTerm) > 0 {
					searchTerm = searchTerm[:len(searchTerm)-1]
				}
			case keyboard.KeyEnter:
				searchMode = false
			default:
				if char != 0 {
					searchTerm += string(char)
				}
			}
			currentPage = 0
			selectedIndex = 0
			continue
		}

		// Handle normal mode input
		switch key {
		case keyboard.KeyEnter:
			if inputBuffer != "" {
				index, err := strconv.Atoi(inputBuffer)
				if err == nil && index >= 1 && index <= len(filteredWorkspaces) {
					return filteredWorkspaces[index-1]["workspace_id"].(string)
				}
				inputBuffer = ""
			} else {
				adjustedIndex := startIndex + selectedIndex
				if adjustedIndex >= 0 && adjustedIndex < len(filteredWorkspaces) {
					return filteredWorkspaces[adjustedIndex]["workspace_id"].(string)
				}
			}
		case keyboard.KeyBackspace, keyboard.KeyBackspace2:
			if len(inputBuffer) > 0 {
				inputBuffer = inputBuffer[:len(inputBuffer)-1]
			}
		}

		// Handle navigation and other commands
		switch char {
		case 'j': // Down
			if selectedIndex < min(pageSize-1, endIndex-startIndex-1) {
				selectedIndex++
			}
		case 'k': // Up
			if selectedIndex > 0 {
				selectedIndex--
			}
		case 'l': // Next page
			currentPage = (currentPage + 1) % totalPages
			selectedIndex = 0
		case 'h': // Previous page
			currentPage = (currentPage - 1 + totalPages) % totalPages
			selectedIndex = 0
		case 'q', 'Q':
			fmt.Println()
			pterm.Error.Println("Workspace selection cancelled.")
			os.Exit(1)
		case '/':
			searchMode = true
			searchTerm = ""
			selectedIndex = 0
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			if !searchMode {
				inputBuffer += string(char)
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

// decodeJWT decodes a JWT token and returns the claims
func decodeJWT(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// validateAndDecodeToken decodes a JWT token and validates its expiration
func validateAndDecodeToken(token string) (map[string]interface{}, error) {
	// Check if token has three parts (header.payload.signature)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format: token must have three parts")
	}

	// Try to decode the payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid token format: failed to decode payload: %v", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid token format: failed to parse payload: %v", err)
	}

	// Check required fields
	requiredFields := []string{"exp", "did"}
	for _, field := range requiredFields {
		if _, ok := claims[field]; !ok {
			return nil, fmt.Errorf("invalid token format: missing required field '%s'", field)
		}
	}

	// Check expiration
	if isTokenExpired(token) {
		return nil, fmt.Errorf("token has expired")
	}

	return claims, nil
}

// clearInvalidTokens removes invalid tokens from the config
func clearInvalidTokens(currentEnv string) error {
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".cfctl", "config.yaml")

	viper.SetConfigFile(configPath)
	if err := viper.ReadInConfig(); err != nil {
		return err
	}

	envPath := fmt.Sprintf("environments.%s", currentEnv)
	envSettings := viper.GetStringMap(envPath)
	if envSettings == nil {
		return nil
	}

	var validTokens []TokenInfo
	if tokensList := viper.Get(fmt.Sprintf("%s.tokens", envPath)); tokensList != nil {
		if tokenList, ok := tokensList.([]interface{}); ok {
			for _, t := range tokenList {
				if tokenMap, ok := t.(map[string]interface{}); ok {
					token := tokenMap["token"].(string)
					if _, err := validateAndDecodeToken(token); err == nil {
						validTokens = append(validTokens, TokenInfo{Token: token})
					}
				}
			}
		}
	}

	// Update config with only valid tokens
	envSettings["tokens"] = validTokens
	viper.Set(envPath, envSettings)
	return viper.WriteConfig()
}

// readTokenFromFile reads a token from the specified file in the environment cache directory
func readTokenFromFile(envDir, tokenType string) (string, error) {
	tokenPath := filepath.Join(envDir, tokenType)
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// getValidTokens checks for existing valid tokens in the environment cache directory
func getValidTokens(currentEnv string) (accessToken, refreshToken string, err error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	envCacheDir := filepath.Join(homeDir, ".cfctl", "cache", currentEnv)

	if refreshToken, err = readTokenFromFile(envCacheDir, "refresh_token"); err == nil {
		claims, err := validateAndDecodeToken(refreshToken)
		if err == nil {
			if exp, ok := claims["exp"].(float64); ok {
				if time.Now().Unix() < int64(exp) {
					if accessToken, err = readTokenFromFile(envCacheDir, "access_token"); err == nil {
						return accessToken, refreshToken, nil
					}
					return accessToken, refreshToken, nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("no valid tokens found")
}
