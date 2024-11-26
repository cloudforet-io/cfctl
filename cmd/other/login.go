package other

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	// Get the home directory of the current user
	homeDir, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Println("Failed to get user home directory:", err)
		exitWithError()
	}

	// Load the main environment file to get the current environment
	viper.SetConfigFile(filepath.Join(homeDir, ".cfctl", "config.yaml"))
	if err := viper.ReadInConfig(); err != nil {
		pterm.Error.Println("Failed to read config.yaml:", err)
		exitWithError()
	}

	// Get the currently selected environment
	currentEnvironment := viper.GetString("environment")
	if currentEnvironment == "" {
		pterm.Error.Println("No environment specified in config.yaml")
		exitWithError()
	}

	// Define paths to look for configuration files
	configPaths := []string{
		filepath.Join(homeDir, ".cfctl", "config.yaml"),
		filepath.Join(homeDir, ".cfctl", "cache", "config.yaml"),
	}

	// Try to find endpoint from each config file
	configFound := false
	for _, configPath := range configPaths {
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err == nil {
			endpointKey := fmt.Sprintf("environments.%s.endpoint", currentEnvironment)
			providedUrl = viper.GetString(endpointKey)
			if providedUrl != "" {
				configFound = true
				break
			}
		}
	}

	if !configFound {
		pterm.Error.Printf("No endpoint found for the current environment '%s' in config.yaml\n", currentEnvironment)
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

		// Show the commands with syntax highlighting
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
	payload := map[string]string{"name": name}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(baseUrl+"/domain/get-auth-info", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch domain ID, status code: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	domainID, ok := result["domain_id"].(string)
	if !ok {
		return "", fmt.Errorf("domain_id not found in response")
	}

	return domainID, nil
}

func issueToken(baseUrl, userID, password, domainID string) (string, string, error) {
	payload := map[string]interface{}{
		"credentials": map[string]string{
			"user_id":  userID,
			"password": password,
		},
		"auth_type":   "LOCAL",
		"timeout":     0,
		"verify_code": "string",
		"domain_id":   domainID,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}

	resp, err := http.Post(baseUrl+"/token/issue", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status code: %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", err
	}

	accessToken, ok := result["access_token"].(string)
	if !ok {
		return "", "", fmt.Errorf("access token not found in response")
	}

	refreshToken, ok := result["refresh_token"].(string)
	if !ok {
		return "", "", fmt.Errorf("refresh token not found in response")
	}

	return accessToken, refreshToken, nil
}

func fetchWorkspaces(baseUrl string, accessToken string) ([]map[string]interface{}, error) {
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
}

func fetchDomainIDAndRole(baseUrl string, accessToken string) (string, string, error) {
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
}

func grantToken(baseUrl, refreshToken, scope, domainID, workspaceID string) (string, error) {
	payload := map[string]interface{}{
		"grant_type":   "REFRESH_TOKEN",
		"token":        refreshToken,
		"scope":        scope,
		"timeout":      86400,
		"domain_id":    domainID,
		"workspace_id": workspaceID,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(baseUrl+"/token/grant", "application/json", bytes.NewBuffer(jsonPayload))
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
}

// saveToken updates the token in the appropriate configuration file based on the environment suffix
func saveToken(newToken string) {
	// Get the home directory and current environment
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
		// For environments ending with '-user', save to cache config
		configPath = filepath.Join(homeDir, ".cfctl", "cache", "config.yaml")
	} else {
		// For other environments, save to main config
		configPath = filepath.Join(homeDir, ".cfctl", "config.yaml")
	}

	// Read the target config file
	file, err := os.Open(configPath)
	if err != nil {
		pterm.Error.Println("Failed to open configuration file:", err)
		exitWithError()
	}
	defer file.Close()

	var newContent []string
	tokenFound := false
	scanner := bufio.NewScanner(file)

	// Find the correct environment section and update its token
	inTargetEnv := false
	indentLevel := 0
	for scanner.Scan() {
		line := scanner.Text()

		// Track environment section
		if strings.HasPrefix(strings.TrimSpace(line), "environments:") {
			newContent = append(newContent, line)
			indentLevel = strings.Index(line, "environments:")
			continue
		}

		// Check if we're in the target environment section
		if strings.HasPrefix(strings.TrimSpace(line), currentEnvironment+":") {
			inTargetEnv = true
			newContent = append(newContent, line)
			continue
		}

		// Check if we're exiting the current environment section
		if inTargetEnv && len(line) > 0 && !strings.HasPrefix(line, strings.Repeat(" ", indentLevel+4)) {
			inTargetEnv = false
		}

		// Update token line if in target environment
		if inTargetEnv && strings.HasPrefix(strings.TrimSpace(line), "token:") {
			newContent = append(newContent, strings.Repeat(" ", indentLevel+4)+"token: "+newToken)
			tokenFound = true
			continue
		}

		newContent = append(newContent, line)
	}

	// Add token if not found in the environment section
	if !tokenFound && inTargetEnv {
		newContent = append(newContent, strings.Repeat(" ", indentLevel+4)+"token: "+newToken)
	}

	// Write the modified content back to the file
	err = ioutil.WriteFile(configPath, []byte(strings.Join(newContent, "\n")), 0644)
	if err != nil {
		pterm.Error.Println("Failed to save updated token to configuration file:", err)
		exitWithError()
	}

	pterm.Success.Println("Token successfully saved to", configPath)
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
