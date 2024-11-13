// config.go

package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

var envFile string

// configCmd represents the config command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage cfctl configuration files",
	Long: `Manage configuration files for cfctl. You can initialize,
switch environments, and display the current configuration.`,
}

// configInitCmd initializes a new environment configuration
var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new environment configuration",
	Run: func(cmd *cobra.Command, args []string) {
		// Retrieve environment name from the flag
		environment, _ := cmd.Flags().GetString("environment")
		importFile, _ := cmd.Flags().GetString("import-file")

		// Prompt for environment if not provided
		if environment == "" {
			environment, _ = pterm.DefaultInteractiveTextInput.WithDefaultText("default").Show("Environment")
			if environment == "" {
				pterm.Error.Println("Environment name must be provided")
				return
			}
		}

		// Ensure environments directory exists
		envDir := filepath.Join(getConfigDir(), "environments")
		if _, err := os.Stat(envDir); os.IsNotExist(err) {
			err = os.MkdirAll(envDir, 0755)
			if err != nil {
				log.Fatalf("Failed to create environments directory: %v", err)
			}
		}

		configPath := filepath.Join(envDir, environment+".yml")
		overwrite := false

		if _, err := os.Stat(configPath); err == nil {
			// Environment file already exists, prompt the user for confirmation
			pterm.Warning.Printf("Environment '%s' already exists. Do you want to overwrite it? (Y/N): ", environment)
			reader := bufio.NewReader(os.Stdin)
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(strings.ToUpper(response))
			if response != "Y" {
				pterm.Info.Println("Operation cancelled by the user.")
				return
			}
			pterm.Info.Printf("Overwriting environment '%s' at '%s'.\n", environment, configPath)
			overwrite = true
		}

		// Create or overwrite the environment file
		file, err := os.Create(configPath)
		if err != nil {
			log.Fatalf("Failed to create environment file: %v", err)
		}
		file.Close()

		// If an import file is provided, write its content into the new environment file
		if importFile != "" {
			viper.SetConfigFile(importFile)
			err := viper.ReadInConfig()
			if err != nil {
				log.Fatalf("Unable to read config file: %v", err)
			}
			err = viper.WriteConfigAs(configPath)
			if err != nil {
				log.Fatalf("Error writing config file: %v", err)
			}
		}

		// Update the ~/.spaceone/environment.yml with the new environment
		envConfigPath := filepath.Join(getConfigDir(), "environment.yml")
		envData := map[string]string{"environment": environment}
		envFile, err := os.Create(envConfigPath)
		if err != nil {
			log.Fatalf("Failed to open environment.yml file: %v", err)
		}
		defer envFile.Close()

		encoder := yaml.NewEncoder(envFile)
		err = encoder.Encode(envData)
		if err != nil {
			log.Fatalf("Failed to update environment.yml file: %v", err)
		}

		// Create short_names.yml if it doesn't exist
		shortNamesFile := filepath.Join(getConfigDir(), "short_names.yml")
		if _, err := os.Stat(shortNamesFile); os.IsNotExist(err) {
			file, err := os.Create(shortNamesFile)
			if err != nil {
				log.Fatalf("Failed to create short_names.yml file: %v", err)
			}
			defer file.Close()
			yamlContent := "# Define your short names here\n# Example:\n# identity.User: 'iu'\n"
			_, err = file.WriteString(yamlContent)
			if err != nil {
				log.Fatalf("Failed to write to short_names.yml file: %v", err)
			}
			pterm.Success.Println("short_names.yml file created successfully.")
		}

		// Update the global config file with the new environment command only if not overwriting
		if !overwrite {
			updateGlobalConfigWithEnvironment(environment)
			pterm.Success.Printf("Environment '%s' initialized at %s\n", environment, configPath)
		}
	},
}

// envCmd manages environment switching and listing
var envCmd = &cobra.Command{
	Use:   "environment",
	Short: "List and manage environments",
	Long:  "List and manage environments",
	Run: func(cmd *cobra.Command, args []string) {
		// Update the global config file with the current list of environments
		updateGlobalConfig()

		switchEnv, _ := cmd.Flags().GetString("switch")
		if switchEnv != "" {
			configPath := filepath.Join(getConfigDir(), "environments", switchEnv+".yml")
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				log.Fatalf("Environment '%s' not found.", switchEnv)
			}

			// Update the ~/.spaceone/environment.yml with the new environment
			envConfigPath := filepath.Join(getConfigDir(), "environment.yml")
			envData := map[string]string{"environment": switchEnv}
			file, err := os.Create(envConfigPath)
			if err != nil {
				log.Fatalf("Failed to open environment.yml file: %v", err)
			}
			defer file.Close()

			encoder := yaml.NewEncoder(file)
			err = encoder.Encode(envData)
			if err != nil {
				log.Fatalf("Failed to update environment.yml file: %v", err)
			}

			// Display only the success message without additional text
			pterm.Success.Printf("Switched to '%s' environment.\n", switchEnv)
			return
		}

		// Check if the -l flag is provided
		listOnly, _ := cmd.Flags().GetBool("list")

		// List environments if the -l flag is set
		if listOnly {
			currentEnv := getCurrentEnvironment()
			envDir := filepath.Join(getConfigDir(), "environments")
			entries, err := os.ReadDir(envDir)
			if err != nil {
				log.Fatalf("Unable to list environments: %v", err)
			}

			pterm.Println("Available Environments:")
			for _, entry := range entries {
				name := entry.Name()
				name = name[:len(name)-len(filepath.Ext(name))] // Remove ".yml" extension
				if name == currentEnv {
					pterm.FgGreen.Printf("  > %s (current)\n", name)
				} else {
					pterm.Printf("  %s\n", name)
				}
			}
			return
		}

		// If -l is not set, show help by default
		cmd.Help()
	},
}

// showCmd displays the current configuration
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the current cfctl configuration",
	Run: func(cmd *cobra.Command, args []string) {
		// Load the current environment from ~/.spaceone/environment.yml
		currentEnv := getCurrentEnvironment()
		if currentEnv == "" {
			log.Fatal("No environment set in ~/.spaceone/environment.yml")
		}

		// Construct the path to the environment's YAML file
		envDir := filepath.Join(getConfigDir(), "environments")
		envFilePath := filepath.Join(envDir, currentEnv+".yml")

		// Check if the environment file exists
		if _, err := os.Stat(envFilePath); os.IsNotExist(err) {
			log.Fatalf("Environment file '%s.yml' does not exist in ~/.spaceone/environments", currentEnv)
		}

		// Load and display the configuration from the environment YAML file
		viper.SetConfigFile(envFilePath)
		err := viper.ReadInConfig()
		if err != nil {
			log.Fatalf("Error reading environment file '%s': %v", envFilePath, err)
		}

		// Get output format from the flag
		output, _ := cmd.Flags().GetString("output")
		configData := viper.AllSettings()

		// Display the configuration in the requested format
		switch output {
		case "json":
			data, err := json.MarshalIndent(configData, "", "  ")
			if err != nil {
				log.Fatalf("Error formatting output as JSON: %v", err)
			}
			fmt.Println(string(data))
		case "yml":
			data, err := yaml.Marshal(configData)
			if err != nil {
				log.Fatalf("Error formatting output as YAML: %v", err)
			}
			fmt.Println(string(data))
		default:
			log.Fatalf("Unsupported output format: %v", output)
		}
	},
}

func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	return filepath.Join(home, ".spaceone")
}

func getCurrentEnvironment() string {
	envConfigPath := filepath.Join(getConfigDir(), "environment.yml")
	viper.SetConfigFile(envConfigPath)

	// Prevent errors if the config file is missing
	_ = viper.ReadInConfig()

	return viper.GetString("environment")
}

func updateGlobalConfig() {
	envDir := filepath.Join(getConfigDir(), "environments")
	entries, err := os.ReadDir(envDir)
	if err != nil {
		log.Fatalf("Unable to list environments: %v", err)
	}

	configPath := filepath.Join(getConfigDir(), "config")

	// Read existing config file content to avoid overwriting
	var content string
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}
		content = string(data)
	}

	// Open the config file for writing
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		log.Fatalf("Failed to open config file: %v", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Add each environment without duplicates
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yml" {
			name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			envEntry := fmt.Sprintf("[%s]\ncfctl environments -s %s\n\n", name, name)
			if !strings.Contains(content, fmt.Sprintf("[%s]", name)) {
				_, err := writer.WriteString(envEntry)
				if err != nil {
					log.Fatalf("Failed to write to config file: %v", err)
				}
			}
		}
	}
	pterm.Success.Println("Global config updated with existing environments.")
}

// updateGlobalConfigWithEnvironment adds or updates the environment entry in the global config file
func updateGlobalConfigWithEnvironment(environment string) {
	configPath := filepath.Join(getConfigDir(), "config")

	// Read the existing config content if it exists
	var content string
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}
		content = string(data)
	}

	// Check if the environment already exists
	envEntry := fmt.Sprintf("[%s]\ncfctl environments -s %s\n", environment, environment)
	if strings.Contains(content, fmt.Sprintf("[%s]", environment)) {
		pterm.Info.Printf("Environment '%s' already exists in the config file.\n", environment)
		return
	}

	// Append the new environment entry
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatalf("Failed to open config file: %v", err)
	}
	defer file.Close()

	// Ensure a newline at the end of existing content
	if len(content) > 0 && content[len(content)-1] != '\n' {
		_, _ = file.WriteString("\n")
	}

	_, err = file.WriteString(envEntry + "\n")
	if err != nil {
		log.Fatalf("Failed to write to config file: %v", err)
	}

	pterm.Success.Printf("Added environment '%s' to global config file.\n", environment)
}

func init() {
	rootCmd.AddCommand(configCmd)

	// Adding subcommands to configCmd
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(envCmd)
	configCmd.AddCommand(showCmd)

	// Defining flags for envCmd
	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")
	envCmd.Flags().BoolP("list", "l", false, "List available environments")

	// Defining flags for showCmd
	showCmd.Flags().StringP("output", "o", "yml", "Output format (yml/json)")

	viper.SetConfigType("yml")
}
