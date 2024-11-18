// config.go

package cmd

import (
	"encoding/json"
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
	Long:  `Initialize a new environment configuration for cfctl by specifying a URL with -u or a local environment name with -l.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Retrieve flags
		environment, _ := cmd.Flags().GetString("environment")
		urlStr, _ := cmd.Flags().GetString("url")
		localEnv, _ := cmd.Flags().GetString("local")

		if urlStr == "" && localEnv == "" {
			cmd.Help()
			return
		}

		var envName string
		if localEnv != "" {
			envName = fmt.Sprintf("%s-user", localEnv)
		} else {
			parsedEnvName, err := parseEnvNameFromURL(urlStr)
			if err != nil {
				pterm.Error.WithShowLineNumber(false).Println("Invalid URL format:", err)
				cmd.Help()
				return
			}
			envName = parsedEnvName
		}

		if environment != "" {
			envName = environment
		}

		// Ensure environments directory exists
		configDir := filepath.Join(getConfigDir(), "environments")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			pterm.Error.WithShowLineNumber(false).Println("Failed to create environments directory:", err)
			return
		}
		envFilePath := filepath.Join(configDir, envName+".yaml")

		// Create an empty environment file if it doesn't already exist
		if _, err := os.Stat(envFilePath); os.IsNotExist(err) {
			file, err := os.Create(envFilePath)
			if err != nil {
				pterm.Error.WithShowLineNumber(false).Println("Failed to create environment file:", err)
				return
			}
			file.Close()
		}

		// Set configuration in config.yaml
		configPath := filepath.Join(getConfigDir(), "config.yaml")
		viper.SetConfigFile(configPath)
		_ = viper.ReadInConfig()

		var baseURL string
		if strings.HasPrefix(envName, "dev") {
			baseURL = "grpc+ssl://identity.api.dev.spaceone.dev:443"
		} else if strings.HasPrefix(envName, "stg") {
			baseURL = "grpc+ssl://identity.api.stg.spaceone.dev:443"
		}

		if baseURL != "" {
			viper.Set(fmt.Sprintf("environments.%s.endpoint", envName), baseURL)
		}

		// Set the current environment
		viper.Set("environment", envName)

		// Write the updated configuration to config.yaml
		if err := viper.WriteConfig(); err != nil {
			pterm.Error.WithShowLineNumber(false).Println("Failed to write updated config.yaml:", err)
			return
		}

		pterm.Success.WithShowLineNumber(false).
			Printfln("Environment '%s' successfully initialized with configuration in '%s/config.yaml'", envName, getConfigDir())
	},
}

// envCmd manages environment switching and listing
var envCmd = &cobra.Command{
	Use:   "environment",
	Short: "List and manage environments",
	Long:  "List and manage environments",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if -s or -r flag is provided
		switchEnv, _ := cmd.Flags().GetString("switch")
		removeEnv, _ := cmd.Flags().GetString("remove")

		// Handle environment switching
		if switchEnv != "" {
			// Check for both .yaml and .yml extensions
			if _, err := os.Stat(filepath.Join(getConfigDir(), "environments", switchEnv+".yaml")); os.IsNotExist(err) {
				if _, err := os.Stat(filepath.Join(getConfigDir(), "environments", switchEnv+".yml")); os.IsNotExist(err) {
					log.Fatalf("Environment '%s' not found.", switchEnv)
				}
			}

			// Update the environment in ~/.spaceone/config.yaml
			configFilePath := filepath.Join(getConfigDir(), "config.yaml")
			viper.SetConfigFile(configFilePath)

			// Read existing config.yaml file to avoid overwriting other fields
			if err := viper.ReadInConfig(); err != nil {
				log.Fatalf("Failed to read config.yaml: %v", err)
			}

			// Update only the environment field
			viper.Set("environment", switchEnv)

			// Write the updated configuration back to config.yaml
			if err := viper.WriteConfig(); err != nil {
				log.Fatalf("Failed to update environment in config.yaml: %v", err)
			}

			// Display success message
			pterm.Success.Printf("Switched to '%s' environment.\n", switchEnv)

			// Update global config after switching environment
			updateGlobalConfig()
			return
		}

		// Handle environment removal with confirmation
		if removeEnv != "" {
			// Check for both .yaml and .yml extensions
			if _, err := os.Stat(filepath.Join(getConfigDir(), "environments", removeEnv+".yaml")); os.IsNotExist(err) {
				if _, err := os.Stat(filepath.Join(getConfigDir(), "environments", removeEnv+".yml")); os.IsNotExist(err) {
					log.Fatalf("Environment '%s' not found.", removeEnv)
				}
			}

			// Ask for confirmation before deletion
			fmt.Printf("Are you sure you want to delete the environment '%s'? (Y/N): ", removeEnv)
			var response string
			fmt.Scanln(&response)
			response = strings.ToLower(strings.TrimSpace(response))

			if response == "y" {
				// Remove the environment file
				os.Remove(filepath.Join(getConfigDir(), "environments", removeEnv+".yaml"))
				os.Remove(filepath.Join(getConfigDir(), "environments", removeEnv+".yml"))

				// Check if this environment is set in config.yaml and clear it if so
				configFilePath := filepath.Join(getConfigDir(), "config.yaml")
				viper.SetConfigFile(configFilePath)
				_ = viper.ReadInConfig() // Read config.yaml

				// Update environment to "no-env" if the deleted environment was the current one
				if viper.GetString("environment") == removeEnv {
					viper.Set("environment", "no-env")
					pterm.Info.WithShowLineNumber(false).Printfln("Cleared current environment (default: %s/config.yaml)", getConfigDir())
				}

				// Remove the environment from the environments map if it exists
				envMap := viper.GetStringMap("environments")
				if _, exists := envMap[removeEnv]; exists {
					delete(envMap, removeEnv)
					viper.Set("environments", envMap)
				}

				// Write the updated configuration back to config.yaml
				if err := viper.WriteConfig(); err != nil {
					log.Fatalf("Failed to update config.yaml: %v", err)
				}

				// Display success message
				pterm.Success.Printf("Removed '%s' environment.\n", removeEnv)

				// Update global config only after successful deletion
				updateGlobalConfig()
			} else {
				pterm.Info.Println("Environment deletion canceled.")
			}
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
				name = strings.TrimSuffix(name, filepath.Ext(name)) // Remove ".yaml" or ".yml" extension
				if name == currentEnv {
					pterm.FgGreen.Printf("  > %s (current)\n", name)
				} else {
					pterm.Printf("  %s\n", name)
				}
			}
			return
		}

		// If no flags are provided, show help by default
		cmd.Help()
	},
}

// showCmd displays the current cfctl configuration
var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Display the current cfctl configuration",
	Run: func(cmd *cobra.Command, args []string) {
		// Load the current environment from ~/.spaceone/config.yaml
		currentEnv := getCurrentEnvironment()
		if currentEnv == "" {
			log.Fatal("No environment set in ~/.spaceone/config.yaml")
		}

		// Construct the path to the environment's YAML file
		envDir := filepath.Join(getConfigDir(), "environments")
		envFilePath := filepath.Join(envDir, currentEnv+".yaml") // Use .yaml as extension

		// Check if the environment file exists
		if _, err := os.Stat(envFilePath); os.IsNotExist(err) {
			log.Fatalf("Environment file '%s.yaml' does not exist in ~/.spaceone/environments", currentEnv)
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

// syncCmd syncs the environments in ~/.spaceone/environments with ~/.spaceone/config.yaml
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync environments from the environments directory to config.yaml",
	Long:  "Sync all environment files from the ~/.spaceone/environments directory to ~/.spaceone/config.yaml",
	Run: func(cmd *cobra.Command, args []string) {
		// Define paths
		envDir := filepath.Join(getConfigDir(), "environments")
		configPath := filepath.Join(getConfigDir(), "config.yaml")

		// Ensure the config file is loaded
		viper.SetConfigFile(configPath)
		_ = viper.ReadInConfig()

		// Iterate over each .yaml file in the environments directory
		entries, err := os.ReadDir(envDir)
		if err != nil {
			log.Fatalf("Unable to read environments directory: %v", err)
		}

		for _, entry := range entries {
			if !entry.IsDir() && (filepath.Ext(entry.Name()) == ".yaml" || filepath.Ext(entry.Name()) == ".yml") {
				envName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))

				// Check if the environment already has a URL; if not, set it to an empty string
				if viper.GetString(fmt.Sprintf("environments.%s.url", envName)) == "" {
					viper.Set(fmt.Sprintf("environments.%s.url", envName), "")
				}
			}
		}

		// Save updated config to config.yaml
		if err := viper.WriteConfig(); err != nil {
			log.Fatalf("Failed to write updated config.yaml: %v", err)
		}

		pterm.Success.Println("Successfully synced environments from environments directory to config.yaml.")
	},
}

// getConfigDir returns the directory where config files are stored
func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	return filepath.Join(home, ".spaceone")
}

// getCurrentEnvironment reads the current environment from ~/.spaceone/config.yaml
func getCurrentEnvironment() string {
	// Set config file path to ~/.spaceone/config.yaml
	configPath := filepath.Join(getConfigDir(), "config.yaml")
	viper.SetConfigFile(configPath)

	// Prevent errors if the config file is missing
	_ = viper.ReadInConfig()

	// Get the environment field from config.yaml
	return viper.GetString("environment")
}

func updateGlobalConfig() {
	pterm.Success.WithShowLineNumber(false).Printfln("Global config updated with existing environments. (default: %s/config.yaml)", getConfigDir())
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
	rootCmd.AddCommand(configCmd)

	// Adding subcommands to configCmd
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(envCmd)
	configCmd.AddCommand(showCmd)
	configCmd.AddCommand(syncCmd)

	// Defining flags for configInitCmd
	configInitCmd.Flags().StringP("environment", "e", "", "Override environment name")
	configInitCmd.Flags().StringP("url", "u", "", "URL for the environment (e.g. cfctl config init -u [URL])")
	configInitCmd.Flags().StringP("local", "l", "", "Local environment name (use instead of URL) (e.g. cfctl config init -l local-[DOMAIN])")

	// Defining flags for envCmd
	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")
	envCmd.Flags().BoolP("list", "l", false, "List available environments")

	// Defining flags for showCmd
	showCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml/json)")

	viper.SetConfigType("yaml")
}
