package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to SpaceONE",
	Long: `A command that allows you to login to SpaceONE.
It will prompt you for your User ID, Password, and Domain ID, then fetch the token.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Check if a token already exists in the configuration
		token := viper.GetString("token")
		if token != "" {
			pterm.Info.Println("Existing token found, attempting to authenticate with saved credentials.")
			if verifyToken(token) {
				pterm.Success.Println("Successfully authenticated with saved token.")
				return
			} else {
				pterm.Warning.Println("Saved token is invalid or expired, proceeding with login.")
			}
		}

		// Prompt for user credentials
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Enter User ID: ")
		userID, _ := reader.ReadString('\n')
		userID = strings.TrimSpace(userID)

		fmt.Print("Enter Password: ")
		password, _ := reader.ReadString('\n')
		password = strings.TrimSpace(password)

		fmt.Print("Enter Domain ID: ")
		domainID, _ := reader.ReadString('\n')
		domainID = strings.TrimSpace(domainID)

		// Read tokenEndpoint from configuration
		tokenEndpoint := viper.GetString("token_endpoint")
		if tokenEndpoint == "" {
			pterm.Error.Println("No token endpoint specified in the configuration file.")
			os.Exit(1)
		}

		// Prepare the request payload for token issue
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
			pterm.Error.Println("Failed to create request payload:", err)
			os.Exit(1)
		}

		// Make the request to get the access token
		resp, err := http.Post(tokenEndpoint, "application/json", bytes.NewBuffer(jsonPayload))
		if err != nil {
			pterm.Error.Println("Failed to make request to token endpoint:", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			pterm.Error.Println("Failed to retrieve token, status code:", resp.StatusCode)
			os.Exit(1)
		}

		// Parse the response
		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		if err != nil {
			pterm.Error.Println("Failed to parse response body:", err)
			os.Exit(1)
		}

		accessToken, ok := result["access_token"].(string)
		if !ok {
			pterm.Error.Println("Access token not found in response")
			os.Exit(1)
		}

		// Save token to configuration
		viper.Set("token", accessToken)
		err = viper.WriteConfig()
		if err != nil {
			pterm.Error.Println("Failed to save configuration file:", err)
			os.Exit(1)
		}

		pterm.Success.Println("Successfully logged in and saved token.")
	},
}

func verifyToken(token string) bool {
	// This function should implement token verification logic, for example by making a request to an endpoint that requires authentication
	// Returning true for simplicity in this example
	return true
}

func init() {
	rootCmd.AddCommand(loginCmd)

	// Load configuration file
	viper.SetConfigName("cfctl")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("$HOME/.spaceone/")
	if err := viper.ReadInConfig(); err != nil {
		pterm.Warning.Println("No configuration file found.")
	}
}
