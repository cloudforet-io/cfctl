/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/

package cmd

import (
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

		if importFile != "" {
			viper.SetConfigFile(importFile)
			err := viper.ReadInConfig()
			if err != nil {
				log.Fatalf("Unable to read config file: %v", err)
			}
		}

		viper.Set("environment", environment)
		configPath := filepath.Join(getConfigDir(), "environments", environment+".yml")
		err := viper.WriteConfigAs(configPath)
		if err != nil {
			log.Fatalf("Error writing config file: %v", err)
		}

		pterm.Success.Printf("Environment %s initialized at %s\n", environment, configPath)
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

// updateGlobalConfig updates the ~/.spaceone/config file with all available environments
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

	var configContent strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		name = name[:len(name)-len(filepath.Ext(name))] // Remove ".yml" extension
		configContent.WriteString(fmt.Sprintf("[%s]\n", name))
		configContent.WriteString(fmt.Sprintf("cfctl environments -s %s\n\n", name))
	}

	_, err = file.WriteString(configContent.String())
	if err != nil {
		log.Fatalf("Failed to write to config file: %v", err)
	}

	pterm.Info.Println("Updated global config file with available environments.")
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

// getConfigDir returns the directory for SpaceONE configuration
func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	return filepath.Join(home, ".spaceone")
}

// getCurrentEnvironment reads the current environment from ~/.spaceone/environment.yml
func getCurrentEnvironment() string {
	envConfigPath := filepath.Join(getConfigDir(), "environment.yml")
	viper.SetConfigFile(envConfigPath)

	err := viper.ReadInConfig()
	if err != nil {
		log.Fatalf("Failed to read environment config file: %v", err)
	}

	return viper.GetString("environment")
}
