// config.go

package other

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

var availableServices = []string{
	"identity", "inventory", "plugin", "repository", "secret",
	"monitoring", "config", "statistics", "notification",
	"cost_analysis", "board", "file_manager", "dashboard",
}

// Separate Viper instances for app and user configurations
var appViper = viper.New()
var userViper = viper.New()

// ConfigCmd represents the config command
var ConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage cfctl configuration files",
	Long: `Manage configuration files for cfctl. You can initialize,
switch environments, and display the current configuration.`,
}

// configInitCmd initializes a new environment configuration
var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new environment configuration",
	Long:  `Initialize a new environment configuration for cfctl by specifying either a URL or a local environment name.`,
}

// configInitURLCmd initializes configuration with a URL
var configInitURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Initialize configuration with a URL",
	Long:  `Specify a URL to initialize the environment configuration.`,
	Args:  cobra.NoArgs,
	Example: `  cfctl config init url -u https://spaceone.spaceone.megazone.io --app
                          or
  cfctl config init url -u https://spaceone.spaceone.megazone.io --user`,
	Run: func(cmd *cobra.Command, args []string) {
		urlStr, _ := cmd.Flags().GetString("url")
		appFlag, _ := cmd.Flags().GetBool("app")
		userFlag, _ := cmd.Flags().GetBool("user")

		if urlStr == "" {
			pterm.Error.Println("The --url flag is required.")
			cmd.Help()
			return
		}
		if !appFlag && !userFlag {
			pterm.Error.Println("You must specify either --app or --user flag.")
			cmd.Help()
			return
		}

		envName, err := parseEnvNameFromURL(urlStr)
		if err != nil {
			pterm.Error.Println("Invalid URL:", err)
			return
		}

		if appFlag {
			envName = fmt.Sprintf("%s-app", envName)
			updateConfig(envName, urlStr, "app")
		} else {
			envName = fmt.Sprintf("%s-user", envName)
			updateConfig(envName, urlStr, "user")
		}
	},
}

// configInitLocalCmd initializes configuration with a local environment
var configInitLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Initialize configuration with a local environment",
	Long:  `Specify a local environment name to initialize the configuration.`,
	Args:  cobra.NoArgs,
	Example: `  cfctl config init local -n local-cloudone --app
                      or
  cfctl config init local -n local-cloudone --user`,
	Run: func(cmd *cobra.Command, args []string) {
		localEnv, _ := cmd.Flags().GetString("name")
		appFlag, _ := cmd.Flags().GetBool("app")
		userFlag, _ := cmd.Flags().GetBool("user")

		if localEnv == "" {
			pterm.Error.Println("The --name flag is required.")
			cmd.Help()
			return
		}
		if !appFlag && !userFlag {
			pterm.Error.Println("You must specify either --app or --user flag.")
			cmd.Help()
			return
		}

		var envName string
		if appFlag {
			envName = fmt.Sprintf("%s-app", localEnv)
			updateConfig(envName, "", "app")
		} else {
			envName = fmt.Sprintf("%s-user", localEnv)
			updateConfig(envName, "", "user")
		}
	},
}

// envCmd manages environment switching and listing
var envCmd = &cobra.Command{
	Use:   "environment",
	Short: "List and manage environments",
	Long:  "List and manage environments",
	Run: func(cmd *cobra.Command, args []string) {
		// Set paths for app and user configurations
		appConfigPath := filepath.Join(GetConfigDir(), "config.yaml")
		userConfigPath := filepath.Join(GetConfigDir(), "cache", "config.yaml")

		// Load app configuration
		if err := loadConfig(appViper, appConfigPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Load user configuration
		if err := loadConfig(userViper, userConfigPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Get current environment (from app config only)
		currentEnv := getCurrentEnvironment(appViper)

		// Check if -s or -r flag is provided
		switchEnv, _ := cmd.Flags().GetString("switch")
		removeEnv, _ := cmd.Flags().GetString("remove")

		// Handle environment switching (app config only)
		if switchEnv != "" {
			// Check environment in both app and user configs
			appEnvMap := appViper.GetStringMap("environments")
			userEnvMap := userViper.GetStringMap("environments")

			if _, existsApp := appEnvMap[switchEnv]; !existsApp {
				if _, existsUser := userEnvMap[switchEnv]; !existsUser {
					home, _ := os.UserHomeDir()
					pterm.Error.Printf("Environment '%s' not found in either %s/.cfctl/config.yaml or %s/.cfctl/cache/config.yaml\n",
						switchEnv, home, home)
					return
				}
			}

			// Update only the environment field in app config
			appViper.Set("environment", switchEnv)

			if err := appViper.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update environment in config.yaml: %v", err)
				return
			}

			pterm.Success.Printf("Switched to '%s' environment.\n", switchEnv)
			updateGlobalConfig()
			return
		}

		// Handle environment removal with confirmation
		if removeEnv != "" {
			// Use appViper or userViper based on where environments are managed
			// Here, assuming environments are managed in appViper
			envMap := appViper.GetStringMap("environments")
			if _, exists := envMap[removeEnv]; !exists {
				log.Fatalf("Environment '%s' not found in config.yaml.", removeEnv)
			}

			// Ask for confirmation before deletion
			fmt.Printf("Are you sure you want to delete the environment '%s'? (Y/N): ", removeEnv)
			var response string
			fmt.Scanln(&response)
			response = strings.ToLower(strings.TrimSpace(response))

			if response == "y" {
				// Remove the environment from the environments map
				delete(envMap, removeEnv)
				appViper.Set("environments", envMap)

				// If the deleted environment was the current one, unset it
				if appViper.GetString("environment") == removeEnv {
					appViper.Set("environment", "")
					pterm.Info.WithShowLineNumber(false).Println("Cleared current environment in config.yaml")
				}

				// Write the updated configuration back to config.yaml
				if err := appViper.WriteConfig(); err != nil {
					log.Fatalf("Failed to update config.yaml: %v", err)
				}

				// Display success message
				pterm.Success.Printf("Removed '%s' environment.\n", removeEnv)
			} else {
				pterm.Info.Println("Environment deletion canceled.")
			}
			return
		}

		// Check if the -l flag is provided
		listOnly, _ := cmd.Flags().GetBool("list")

		// List environments if the -l flag is set
		if listOnly {
			// Get environment maps from both app and user configs
			appEnvMap := appViper.GetStringMap("environments")
			userEnvMap := userViper.GetStringMap("environments")

			// Map to store all unique environments
			allEnvs := make(map[string]bool)

			// Add app environments
			for envName := range appEnvMap {
				allEnvs[envName] = true
			}

			// Add user environments
			for envName := range userEnvMap {
				allEnvs[envName] = true
			}

			if len(allEnvs) == 0 {
				pterm.Println("No environments found in config files")
				return
			}

			pterm.Println("Available Environments:")

			// Print environments with their source and current status
			for envName := range allEnvs {
				if envName == currentEnv {
					pterm.FgGreen.Printf("  %s (current)\n", envName)
				} else {
					if _, isApp := appEnvMap[envName]; isApp {
						pterm.Printf("  %s (app)\n", envName)
					} else {
						pterm.Printf("  %s (user)\n", envName)
					}
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
		// Assume showCmd operates on the app config
		configPath := filepath.Join(GetConfigDir(), "config.yaml")
		v := appViper

		// Load or create the config file
		if err := loadConfig(v, configPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		currentEnv := getCurrentEnvironment(v)
		if currentEnv == "" {
			log.Fatal("No environment set in ~/.cfctl/config.yaml")
		}

		envConfig := v.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))
		if len(envConfig) == 0 {
			log.Fatalf("Environment '%s' not found in config.yaml", currentEnv)
		}

		output, _ := cmd.Flags().GetString("output")

		switch output {
		case "json":
			data, err := json.MarshalIndent(envConfig, "", "  ")
			if err != nil {
				log.Fatalf("Error formatting output as JSON: %v", err)
			}
			fmt.Println(string(data))
		case "yaml":
			data, err := yaml.Marshal(envConfig)
			if err != nil {
				log.Fatalf("Error formatting output as YAML: %v", err)
			}
			fmt.Println(string(data))
		default:
			log.Fatalf("Unsupported output format: %v", output)
		}
	},
}

// configEndpointCmd updates the endpoint for the current environment
var configEndpointCmd = &cobra.Command{
	Use:   "endpoint",
	Short: "Set the endpoint for the current environment",
	Long: `Update the endpoint for the current environment based on the specified service.
If the service is not 'identity', the proxy setting will be updated to false.

Available Services:
  identity, inventory, plugin, repository, secret, monitoring, config, statistics,
  notification, cost_analysis, board, file_manager, dashboard`,
	Run: func(cmd *cobra.Command, args []string) {
		service, _ := cmd.Flags().GetString("service")
		if service == "" {
			pterm.Error.Println("Please specify a service using -s or --service.")
			pterm.Println()
			pterm.DefaultBox.WithTitle("Available Services").
				WithRightPadding(1).WithLeftPadding(1).WithTopPadding(0).WithBottomPadding(0).
				Println(strings.Join(availableServices, "\n"))
			return
		}

		// Validate the service name
		isValidService := false
		for _, validService := range availableServices {
			if service == validService {
				isValidService = true
				break
			}
		}

		if !isValidService {
			pterm.Error.Printf("Invalid service '%s'.\n", service)
			pterm.Println()
			pterm.DefaultBox.WithTitle("Available Services").
				WithRightPadding(1).WithLeftPadding(1).WithTopPadding(0).WithBottomPadding(0).
				Println(strings.Join(availableServices, "\n"))
			return
		}

		// Assume configEndpointCmd operates on the app config
		configPath := filepath.Join(GetConfigDir(), "config.yaml")
		v := appViper

		// Load or create the config file
		if err := loadConfig(v, configPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		currentEnv := getCurrentEnvironment(v)
		if currentEnv == "" {
			pterm.Error.Println("No environment is set. Please initialize or switch to an environment.")
			return
		}

		// Determine prefix from the current environment
		var prefix string
		if strings.HasPrefix(currentEnv, "dev-") {
			prefix = "dev"
		} else if strings.HasPrefix(currentEnv, "stg-") {
			prefix = "stg"
		} else {
			pterm.Error.Printf("Unsupported environment prefix for '%s'.\n", currentEnv)
			return
		}

		// Construct new endpoint
		newEndpoint := fmt.Sprintf("grpc+ssl://%s.api.%s.spaceone.dev:443", service, prefix)

		// Update endpoint
		v.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), newEndpoint)

		// Update proxy based on service
		if service != "identity" {
			v.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), false)
		} else {
			v.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), true)
		}

		// Save updated config
		if err := v.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to update config.yaml: %v\n", err)
			return
		}

		pterm.Success.Printf("Updated endpoint for '%s' to '%s'.\n", currentEnv, newEndpoint)
	},
}

// GetConfigDir returns the directory where config files are stored
func GetConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	return filepath.Join(home, ".cfctl")
}

// loadConfig ensures that the config directory and config file exist.
// It initializes the config file with default values if it does not exist.
func loadConfig(v *viper.Viper, configPath string) error {
	// Ensure the config directory exists
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory '%s': %w", configDir, err)
	}

	// Set the config file
	v.SetConfigFile(configPath)

	// Check if the config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Initialize with default values
		v.Set("environments", map[string]interface{}{})
		v.Set("environment", "")

		// Write the default config to the file
		if err := v.WriteConfigAs(configPath); err != nil {
			return fmt.Errorf("failed to create config file '%s': %w", configPath, err)
		}

		// Read the newly created config file
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read newly created config file '%s': %w", configPath, err)
		}
	} else {
		// Read the existing config file
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read config file '%s': %w", configPath, err)
		}
	}

	return nil
}

// getCurrentEnvironment reads the current environment from the given Viper instance
func getCurrentEnvironment(v *viper.Viper) string {
	return v.GetString("environment")
}

// updateGlobalConfig prints a success message for global config update
func updateGlobalConfig() {
	configPath := filepath.Join(GetConfigDir(), "config.yaml")
	v := viper.New()

	v.SetConfigFile(configPath)

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			pterm.Success.WithShowLineNumber(false).Printfln("Global config updated with existing environments. (default: %s/config.yaml)", GetConfigDir())
			return
		}
		pterm.Warning.Printf("Warning: Could not read global config: %v\n", err)
		return
	}

	pterm.Success.WithShowLineNumber(false).Printfln("Global config updated with existing environments. (default: %s/config.yaml)", GetConfigDir())
}

// parseEnvNameFromURL parses environment name from the given URL and validates based on URL structure
func parseEnvNameFromURL(urlStr string) (string, error) {
	if !strings.Contains(urlStr, "://") {
		urlStr = "https://" + urlStr
	}
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

// updateConfig handles the actual configuration update
func updateConfig(envName, baseURL, mode string) {
	var configPath string
	var v *viper.Viper

	if mode == "app" {
		configPath = filepath.Join(GetConfigDir(), "config.yaml")
		v = appViper
	} else {
		configPath = filepath.Join(GetConfigDir(), "cache", "config.yaml")
		v = userViper
	}

	// Load or create the config file
	if err := loadConfig(v, configPath); err != nil {
		pterm.Error.Println(err)
		return
	}

	// Check if the environment already exists
	if v.IsSet(fmt.Sprintf("environments.%s", envName)) {
		pterm.Warning.Printf("Environment '%s' already exists. Do you want to overwrite it? (y/n): ", envName)

		// Get user input
		var response string
		fmt.Scanln(&response)
		response = strings.ToLower(strings.TrimSpace(response))

		// If user selects "n", exit the function
		if response != "y" {
			pterm.Info.Printf("Skipping initialization for '%s'. No changes were made.\n", envName)
			return
		}
	}

	var newEndpoint string
	if baseURL != "" {
		var err error
		newEndpoint, err = constructEndpoint(baseURL)
		if err != nil {
			pterm.Error.Println("Failed to construct endpoint:", err)
			return
		}
	} else {
		newEndpoint = "grpc://localhost:50051"
	}

	// Set default values for the environment
	v.Set(fmt.Sprintf("environments.%s.endpoint", envName), newEndpoint)
	v.Set(fmt.Sprintf("environments.%s.token", envName), "")

	if strings.Contains(newEndpoint, "identity") {
		v.Set(fmt.Sprintf("environments.%s.proxy", envName), true)
	} else {
		v.Set(fmt.Sprintf("environments.%s.proxy", envName), false)
	}

	// Write the updated configuration back to config.yaml or cache/config.yaml
	if err := v.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to write configuration: %v\n", err)
		return
	}

	// Load the app configuration to update the 'environment' field
	appConfigPath := filepath.Join(GetConfigDir(), "config.yaml")
	if err := loadConfig(appViper, appConfigPath); err != nil {
		pterm.Error.Println("Failed to load app config:", err)
		return
	}

	// Set the 'environment' field in the app config
	appViper.Set("environment", envName)

	// Write the updated app configuration
	if err := appViper.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to write app config: %v\n", err)
		return
	}

	// If initializing a user environment, remove 'environment' field from user config
	if mode == "user" {
		err := removeEnvironmentField(v)
		if err != nil {
			pterm.Error.Printf("Failed to remove environment from user config: %v\n", err)
			return
		}
	}

	pterm.Success.Printf("Environment '%s' successfully initialized.\n", envName)
}

// removeEnvironmentField removes the 'environment' field from the given Viper instance
func removeEnvironmentField(v *viper.Viper) error {
	config := make(map[string]interface{})
	if err := v.Unmarshal(&config); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	delete(config, "environment")

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal updated config: %w", err)
	}

	if err := os.WriteFile(v.ConfigFileUsed(), data, 0644); err != nil {
		return fmt.Errorf("failed to write updated config to file: %w", err)
	}

	return nil
}

// constructEndpoint generates the gRPC endpoint string from baseURL
func constructEndpoint(baseURL string) (string, error) {
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	hostname := parsedURL.Hostname()

	prefix := ""
	// Determine the prefix based on the hostname
	switch {
	case strings.Contains(hostname, ".dev.spaceone.dev"):
		prefix = "dev"
	case strings.Contains(hostname, ".stg.spaceone.dev"):
		prefix = "stg"
	case strings.Contains(hostname, ".spaceone.megazone.io"):
		prefix = "prd"
	default:
		return "", fmt.Errorf("unknown environment prefix in URL: %s", hostname)
	}

	// Extract the service from the hostname
	service := strings.Split(hostname, ".")[0]
	if service == "" {
		return "", fmt.Errorf("unable to determine service from URL: %s", hostname)
	}

	// Construct the endpoint dynamically based on the service
	newEndpoint := fmt.Sprintf("grpc+ssl://%s.api.%s.spaceone.dev:443", service, prefix)
	return newEndpoint, nil
}

func init() {
	configDir := GetConfigDir()
	configPath := filepath.Join(configDir, "config.yaml")
	cacheConfigPath := filepath.Join(configDir, "cache", "config.yaml")

	ConfigCmd.AddCommand(configInitCmd)
	ConfigCmd.AddCommand(envCmd)
	ConfigCmd.AddCommand(showCmd)
	ConfigCmd.AddCommand(configEndpointCmd)
	configInitCmd.AddCommand(configInitURLCmd)
	configInitCmd.AddCommand(configInitLocalCmd)

	configInitCmd.Flags().StringP("environment", "e", "", "Override environment name")

	configInitURLCmd.Flags().StringP("url", "u", "", "URL for the environment")
	configInitURLCmd.Flags().Bool("app", false, fmt.Sprintf("Initialize as application configuration (config stored at %s)", configPath))
	configInitURLCmd.Flags().Bool("user", false, fmt.Sprintf("Initialize as user-specific configuration (config stored at %s)", cacheConfigPath))

	configInitLocalCmd.Flags().StringP("name", "n", "", "Local environment name for the environment")
	configInitLocalCmd.Flags().Bool("app", false, fmt.Sprintf("Initialize as application configuration (config stored at %s)", configPath))
	configInitLocalCmd.Flags().Bool("user", false, fmt.Sprintf("Initialize as user-specific configuration (config stored at %s)", cacheConfigPath))

	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")
	envCmd.Flags().BoolP("list", "l", false, "List available environments")

	showCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml/json)")

	configEndpointCmd.Flags().StringP("service", "s", "", "Service to set the endpoint for")

	// No need to set global Viper config type since we are using separate instances
}
