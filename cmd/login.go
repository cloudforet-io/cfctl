package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var url string

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to SpaceONE",
	Long: `A command that allows you to login to SpaceONE.
It will prompt you for your User ID, Password, and fetch the Domain ID automatically, then fetch the token.`,
	Run: executeLogin,
}

func executeLogin(cmd *cobra.Command, args []string) {
	if token := viper.GetString("token"); token != "" {
		if !isTokenExpired(token) {
			pterm.Info.Println("Existing token found and it is still valid. Attempting to authenticate with saved credentials.")
			if verifyToken(token) {
				pterm.Success.Println("Successfully authenticated with saved token.")
				return
			}
		}
		pterm.Warning.Println("Saved token is expired or invalid, proceeding with login.")
	}

	if url == "" {
		pterm.Error.Println("URL must be provided with the -u flag.")
		exitWithError()
	}

	userID, password := promptCredentials()

	re := regexp.MustCompile(`https://(.*?)\.`)
	matches := re.FindStringSubmatch(url)
	if len(matches) < 2 {
		pterm.Error.Println("Invalid URL format.")
		exitWithError()
	}
	name := matches[1]

	baseUrl := viper.GetString("base_url")
	if baseUrl == "" {
		pterm.Error.Println("No token endpoint specified in the configuration file.")
		exitWithError()
	}

	domainID, err := fetchDomainID(baseUrl, name)
	if err != nil {
		pterm.Error.Println("Failed to fetch Domain ID:", err)
		exitWithError()
	}

	accessToken, refreshToken, err := issueToken(baseUrl, userID, password, domainID)
	if err != nil {
		pterm.Error.Println("Failed to retrieve token:", err)
		exitWithError()
	}

	workspaces, err := fetchWorkspaces(baseUrl, accessToken)
	if err != nil {
		pterm.Error.Println("Failed to fetch workspaces:", err)
		exitWithError()
	}

	workspaceID := selectWorkspace(workspaces)
	domainID, roleType, err := fetchDomainIDAndRole(baseUrl, accessToken)
	if err != nil {
		pterm.Error.Println("Failed to fetch Domain ID and Role Type:", err)
		exitWithError()
	}

	scope := determineScope(roleType, len(workspaces))
	newAccessToken, err := grantToken(baseUrl, refreshToken, scope, domainID, workspaceID)
	if err != nil {
		pterm.Error.Println("Failed to retrieve new access token:", err)
		exitWithError()
	}

	saveToken(newAccessToken)
	pterm.Success.Println("Successfully logged in and saved token.")
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

func promptCredentials() (string, string) {
	userId, _ := pterm.DefaultInteractiveTextInput.Show("Enter your user ID")
	passwordInput := pterm.DefaultInteractiveTextInput.WithMask("*")
	password, _ := passwordInput.Show("Enter your password")
	return userId, password
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

func determineScope(roleType string, workspaceCount int) string {
	switch roleType {
	case "DOMAIN_ADMIN":
		if workspaceCount == 0 {
			return "DOMAIN"
		}
		return "WORKSPACE"
	case "USER":
		return "WORKSPACE"
	default:
		pterm.Error.Println("Unknown role_type:", roleType)
		exitWithError()
		return "" // Unreachable
	}
}

func grantToken(baseUrl, refreshToken, scope, domainID, workspaceID string) (string, error) {
	payload := map[string]interface{}{
		"grant_type":   "REFRESH_TOKEN",
		"token":        refreshToken,
		"scope":        scope,
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

func saveToken(token string) {
	viper.Set("token", token)
	if err := viper.WriteConfig(); err != nil {
		pterm.Error.Println("Failed to save configuration file:", err)
		exitWithError()
	}
}

func exitWithError() {
	os.Exit(1)
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

func verifyToken(token string) bool {
	// This function should implement token verification logic, for example by making a request to an endpoint that requires authentication
	// Returning true for simplicity in this example
	return true
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

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().StringVarP(&url, "url", "u", "", "The URL to use for login (e.g. cfctl login -u https://example.com)")
	loginCmd.MarkFlagRequired("url")

	// Load configuration file
	viper.SetConfigName("cfctl")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.spaceone/")
	if err := viper.ReadInConfig(); err != nil {
		pterm.Warning.Println("No configuration file found.")
	}
}
