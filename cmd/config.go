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

		for _, entry := range entries {
			name := entry.Name()
			name = name[:len(name)-len(filepath.Ext(name))] // Remove ".yaml" extension
			if name == currentEnv {
				pterm.Info.Println(name + " (current)")
			} else {
				pterm.Println(name)
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
		case "yaml":
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

func init() {
	rootCmd.AddCommand(configCmd)

	// Adding subcommands to configCmd
	configCmd.AddCommand(initCmd)
	configCmd.AddCommand(envCmd)
	configCmd.AddCommand(showCmd)

	// Defining flags
	initCmd.Flags().StringP("environment", "e", "", "Name of the environment (required)")
	initCmd.Flags().StringP("import-file", "f", "", "Path to an import configuration file")
	initCmd.MarkFlagRequired("environment")

	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	showCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml/json)")

	viper.SetConfigType("yaml")
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
