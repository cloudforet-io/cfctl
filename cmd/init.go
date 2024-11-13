/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new environment configuration",
	Long:  `Initialize a new environment configuration for cfctl by specifying a URL.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Retrieve flags
		environment, _ := cmd.Flags().GetString("environment")
		urlStr, _ := cmd.Flags().GetString("url")

		// Ensure URL has a scheme; default to "https" if missing
		if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
			urlStr = "https://" + urlStr
		}

		// Parse environment name from URL
		envName, err := parseEnvNameFromURL(urlStr)
		if err != nil {
			pterm.Error.WithShowLineNumber(false).Println("Invalid URL format:", err)
			return
		}

		// If environment name is provided, override the parsed name
		if environment != "" {
			envName = environment
		}

		// Ensure environments directory exists
		configPath := filepath.Join(getInitConfigDir(), "config.yaml")

		// Load existing config if it exists
		viper.SetConfigFile(configPath)
		_ = viper.ReadInConfig()

		// Add or update the environment entry in viper
		viper.Set(fmt.Sprintf("environments.%s.url", envName), urlStr)

		// Set the default environment to the new envName
		viper.Set("environment", envName)

		// Serialize config data with 2-space indentation
		configData := viper.AllSettings()
		yamlData, err := yaml.Marshal(configData)
		if err != nil {
			pterm.Error.WithShowLineNumber(false).Println("Failed to encode YAML data:", err)
			return
		}

		// Write the serialized YAML to file with 2-space indentation
		file, err := os.Create(configPath)
		if err != nil {
			pterm.Error.WithShowLineNumber(false).Println("Failed to write to config.yaml:", err)
			return
		}
		defer file.Close()

		if _, err := file.Write(yamlData); err != nil {
			pterm.Error.WithShowLineNumber(false).Println("Failed to write YAML data to file:", err)
			return
		}

		pterm.Success.WithShowLineNumber(false).Printfln("Environment '%s' successfully initialized and set as the current environment in config.yaml", envName)

		// After successfully writing to config.yaml, create the environment-specific YAML file
		envFilePath := filepath.Join(getInitConfigDir(), "environments", fmt.Sprintf("%s.yaml", envName))

		// Ensure the environments directory exists
		environmentsDir := filepath.Dir(envFilePath)
		if _, err := os.Stat(environmentsDir); os.IsNotExist(err) {
			os.MkdirAll(environmentsDir, os.ModePerm)
		}

		// Create a blank environment-specific file if it doesn't exist
		if _, err := os.Stat(envFilePath); os.IsNotExist(err) {
			file, err := os.Create(envFilePath)
			if err != nil {
				pterm.Error.WithShowLineNumber(false).Println("Failed to create environment file:", err)
				return
			}
			defer file.Close()
			pterm.Success.WithShowLineNumber(false).Printfln("Created environment-specific file: %s", envFilePath)
		} else {
			pterm.Info.WithShowLineNumber(false).Printfln("Environment file already exists: %s", envFilePath)
		}
	},
}

// parseEnvNameFromURL parses environment name from the given URL and validates based on URL structure
func parseEnvNameFromURL(urlStr string) (string, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	hostname := parsedURL.Hostname()

	// Check for `prd` environment pattern
	if strings.HasSuffix(hostname, "spaceone.megazone.io") {
		re := regexp.MustCompile(`^(.*?)\.spaceone`)
		matches := re.FindStringSubmatch(hostname)
		if len(matches) == 2 {
			return fmt.Sprintf("prd-%s", matches[1]), nil
		}
	}

	// Check for `dev` environment pattern
	if strings.HasSuffix(hostname, "console.dev.spaceone.dev") {
		re := regexp.MustCompile(`(.*)\.console\.dev\.spaceone\.dev`)
		matches := re.FindStringSubmatch(hostname)
		if len(matches) == 2 {
			return fmt.Sprintf("dev-%s", matches[1]), nil
		}
		pterm.Error.WithShowLineNumber(false).Println("Invalid URL format for dev environment. Expected format: '<prefix>.console.dev.spaceone.dev'")
		return "", fmt.Errorf("invalid dev URL format")
	}

	// Check for `stg` environment pattern
	if strings.HasSuffix(hostname, "console.stg.spaceone.dev") {
		re := regexp.MustCompile(`(.*)\.console\.stg\.spaceone\.dev`)
		matches := re.FindStringSubmatch(hostname)
		if len(matches) == 2 {
			return fmt.Sprintf("stg-%s", matches[1]), nil
		}
		pterm.Error.WithShowLineNumber(false).Println("Invalid URL format for stg environment. Expected format: '<prefix>.console.stg.spaceone.dev'")
		return "", fmt.Errorf("invalid stg URL format")
	}

	return "", fmt.Errorf("URL does not match any known environment patterns")
}

func init() {
	rootCmd.AddCommand(initCmd)

	// Define flags for the init command
	initCmd.Flags().StringP("environment", "e", "", "Override environment name")
	initCmd.Flags().StringP("url", "u", "", "URL for the environment")
	initCmd.MarkFlagRequired("url") // Ensure URL is mandatory
}

// getInitConfigDir returns the directory where config files are stored
func getInitConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.WithShowLineNumber(false).Println("Unable to find home directory:", err)
		log.Fatal(err)
	}
	return filepath.Join(home, ".spaceone")
}
