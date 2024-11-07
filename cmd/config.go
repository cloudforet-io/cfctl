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
	"sync"

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

// initCmd initializes a new environment configuration
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new environment configuration",
	Run: func(cmd *cobra.Command, args []string) {
		environment, _ := cmd.Flags().GetString("environment")
		importFile, _ := cmd.Flags().GetString("import-file")

		if environment == "" {
			log.Fatalf("Environment name must be provided")
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
	Short: "Manage and switch environments",
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

			pterm.Success.Printf("Switched to '%s' environment.\n", switchEnv)
			return
		}

		currentEnv := getCurrentEnvironment()
		envDir := filepath.Join(getConfigDir(), "environments")
		entries, err := os.ReadDir(envDir)
		if err != nil {
			log.Fatalf("Unable to list environments: %v", err)
		}

		pterm.Println("Available Environments:\n")
		for _, entry := range entries {
			name := entry.Name()
			name = name[:len(name)-len(filepath.Ext(name))] // Remove ".yml" extension
			if name == currentEnv {
				pterm.FgGreen.Printf("  > %s (current)\n", name)
			} else {
				pterm.Printf("  %s\n", name)
			}
		}
	},
}

// showCmd displays the current configuration
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the current cfctl configuration",
	Run: func(cmd *cobra.Command, args []string) {
		output, _ := cmd.Flags().GetString("output")
		configData := viper.AllSettings()

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
				log.Fatalf("Error formatting output as yml: %v", err)
			}
			fmt.Println(string(data))
		default:
			log.Fatalf("Unsupported output format: %v", output)
		}
	},
}

func init() {
	rootCmd.AddCommand(configCmd)

	// Adding subcommands to configCmd
	configCmd.AddCommand(initCmd)
	configCmd.AddCommand(envCmd)
	configCmd.AddCommand(showCmd)

	// Defining flags for initCmd
	initCmd.Flags().StringP("environment", "e", "", "Name of the environment (required)")
	initCmd.Flags().StringP("import-file", "f", "", "Path to an import configuration file")
	initCmd.MarkFlagRequired("environment")

	// Defining flags for envCmd
	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")

	// Defining flags for showCmd
	showCmd.Flags().StringP("output", "o", "yml", "Output format (yml/json)")

	viper.SetConfigType("yml")
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

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatalf("Failed to read environment config file: %v", err)
	}

	return viper.GetString("environment")
}

func updateGlobalConfig() {
	envDir := filepath.Join(getConfigDir(), "environments")
	entries, err := os.ReadDir(envDir)
	if err != nil {
		log.Fatalf("Unable to list environments: %v", err)
	}

	configPath := filepath.Join(getConfigDir(), "config")
	file, err := os.Create(configPath)
	if err != nil {
		log.Fatalf("Failed to open config file: %v", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	var wg sync.WaitGroup
	existingEnvironments := make(map[string]bool)

	for _, entry := range entries {
		wg.Add(1)
		go func(entry os.DirEntry) {
			defer wg.Done()
			name := entry.Name()
			name = name[:len(name)-len(filepath.Ext(name))] // Remove ".yml" extension
			existingEnvironments[name] = true
			writer.WriteString(fmt.Sprintf("[%s]\n", name))
			writer.WriteString(fmt.Sprintf("cfctl environments -s %s\n\n", name))
		}(entry)
	}

	wg.Wait()

	pterm.Info.Println("Updated global config file with available environments.")
}

// updateGlobalConfigWithEnvironment adds the new environment command to the global config file
func updateGlobalConfigWithEnvironment(environment string) {
	configPath := filepath.Join(getConfigDir(), "config")

	var file *os.File
	var err error
	var message string
	var isFileCreated bool

	// Check if the config file already exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create a new config file if it does not exist
		file, err = os.Create(configPath)
		if err != nil {
			log.Fatalf("Failed to create config file: %v", err)
		}
		message = fmt.Sprintf("Created global config file with environment '%s'.\n", environment)
		isFileCreated = true
	} else {
		// Read the existing config file to check for duplicates
		content, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}
		if strings.Contains(string(content), fmt.Sprintf("[%s]", environment)) {
			pterm.Info.Printf("Environment '%s' already exists in the config file.\n", environment)
			return
		}
		// Open the existing config file for appending
		file, err = os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			log.Fatalf("Failed to open config file: %v", err)
		}

		// Ensure the last line ends with a newline
		if len(content) > 0 && content[len(content)-1] != '\n' {
			_, err = file.WriteString("\n")
			if err != nil {
				log.Fatalf("Failed to write newline to config file: %v", err)
			}
		}
		message = fmt.Sprintf("Added environment '%s' to global config file.\n", environment)
		isFileCreated = false
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Append the new environment to the config file
	_, err = writer.WriteString(fmt.Sprintf("[%s]\ncfctl environments -s %s\n\n", environment, environment))
	if err != nil {
		log.Fatalf("Failed to write to config file: %v", err)
	}

	if isFileCreated {
		pterm.Info.Print(message)
	} else {
		pterm.Success.Print(message)
	}
}