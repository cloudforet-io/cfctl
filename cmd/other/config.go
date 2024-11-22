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

var configInitURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Initialize configuration with a URL",
	Long:  `Specify a URL to initialize the environment configuration.`,
	Args:  cobra.NoArgs,
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

		// Add suffix based on the flag
		if appFlag {
			envName = fmt.Sprintf("%s-app", envName)
			updateConfig(envName, urlStr, "app")
		} else {
			envName = fmt.Sprintf("%s-user", envName)
			updateConfig(envName, urlStr, "user")
		}
	},
}

var configInitLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Initialize configuration with a local environment",
	Long:  `Specify a local environment name to initialize the configuration.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		localEnv, _ := cmd.Flags().GetString("local")
		appFlag, _ := cmd.Flags().GetBool("app")
		userFlag, _ := cmd.Flags().GetBool("user")

		if localEnv == "" {
			pterm.Error.Println("The --local flag is required.")
			cmd.Help()
			return
		}
		if !appFlag && !userFlag {
			pterm.Error.Println("You must specify either --app or --user flag.")
			cmd.Help()
			return
		}

		// Add suffix based on the flag
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
		// Check if -s or -r flag is provided
		switchEnv, _ := cmd.Flags().GetString("switch")
		removeEnv, _ := cmd.Flags().GetString("remove")

		// Handle environment switching
		if switchEnv != "" {
			// Load config.yaml
			configFilePath := filepath.Join(GetConfigDir(), "config.yaml")
			viper.SetConfigFile(configFilePath)

			// Read existing config.yaml file
			if err := viper.ReadInConfig(); err != nil {
				log.Fatalf("Failed to read config.yaml: %v", err)
			}

			// Check if the environment exists in the environments map
			home, err := os.UserHomeDir()
			if err != nil {
				log.Fatalf("Unable to find home directory: %v", err)
			}
			envMap := viper.GetStringMap("environments")
			if _, exists := envMap[switchEnv]; !exists {
				//log.Fatalf("Environment '%s' not found in config.yaml.", switchEnv)
				pterm.Error.Printf("Environment '%s' not found in %s/.cfctl/config.yaml\n", switchEnv, home)
				return
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
			// Load config.yaml
			configFilePath := filepath.Join(GetConfigDir(), "config.yaml")
			viper.SetConfigFile(configFilePath)
			if err := viper.ReadInConfig(); err != nil {
				log.Fatalf("Failed to read config.yaml: %v", err)
			}

			// Check if the environment exists in the environments map
			envMap := viper.GetStringMap("environments")
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
				viper.Set("environments", envMap)

				// If the deleted environment was the current one, unset it
				if viper.GetString("environment") == removeEnv {
					viper.Set("environment", "")
					pterm.Info.WithShowLineNumber(false).Printfln("Cleared current environment in config.yaml")
				}

				// Write the updated configuration back to config.yaml
				if err := viper.WriteConfig(); err != nil {
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
			currentEnv := getCurrentEnvironment()

			// Load config.yaml
			configPath := filepath.Join(GetConfigDir(), "config.yaml")
			viper.SetConfigFile(configPath)
			if err := viper.ReadInConfig(); err != nil {
				log.Fatalf("Failed to read config.yaml: %v", err)
			}

			envMap := viper.GetStringMap("environments")
			if len(envMap) == 0 {
				pterm.Println("No environments found in config.yaml")
				return
			}

			pterm.Println("Available Environments:")
			for envName := range envMap {
				if envName == currentEnv {
					pterm.FgGreen.Printf("  %s (current)\n", envName)
				} else {
					pterm.Printf("  %s\n", envName)
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
		currentEnv := getCurrentEnvironment()
		if currentEnv == "" {
			log.Fatal("No environment set in ~/.cfctl/config.yaml")
		}

		configPath := filepath.Join(GetConfigDir(), "config.yaml")
		viper.SetConfigFile(configPath)
		err := viper.ReadInConfig()
		if err != nil {
			log.Fatalf("Failed to read config.yaml: %v", err)
		}

		envConfig := viper.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))
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

		currentEnv := getCurrentEnvironment()
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

		// Load config
		configPath := filepath.Join(GetConfigDir(), "config.yaml")
		viper.SetConfigFile(configPath)
		if err := viper.ReadInConfig(); err != nil {
			pterm.Error.Printf("Failed to read config.yaml: %v\n", err)
			return
		}

		// Update endpoint
		viper.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), newEndpoint)

		// Update proxy based on service
		if service != "identity" {
			viper.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), false)
		} else {
			viper.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), true)
		}

		// Save updated config
		if err := viper.WriteConfig(); err != nil {
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

// getCurrentEnvironment reads the current environment from ~/.cfctl/config.yaml
func getCurrentEnvironment() string {
	// Set config file path to ~/.spaceone/config.yaml
	configPath := filepath.Join(GetConfigDir(), "config.yaml")
	viper.SetConfigFile(configPath)

	// Prevent errors if the config file is missing
	_ = viper.ReadInConfig()

	// Get the environment field from config.yaml
	return viper.GetString("environment")
}

func updateGlobalConfig() {
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
	if mode == "app" {
		configPath = filepath.Join(GetConfigDir(), "config.yaml")
	} else {
		cacheDir := filepath.Join(GetConfigDir(), "cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			pterm.Error.Println("Failed to create cache directory:", err)
			return
		}
		configPath = filepath.Join(cacheDir, "config.yaml")
	}

	viper.SetConfigFile(configPath)
	_ = viper.ReadInConfig()

	// Check if the environment already exists
	if viper.IsSet(fmt.Sprintf("environments.%s", envName)) {
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

	// Construct a new endpoint based on baseURL
	newEndpoint, err := constructEndpoint(baseURL)
	if err != nil {
		pterm.Error.Println("Failed to construct endpoint:", err)
		return
	}

	// Set default values for the environment
	viper.Set(fmt.Sprintf("environments.%s.endpoint", envName), newEndpoint)
	viper.Set(fmt.Sprintf("environments.%s.token", envName), "")
	viper.Set(fmt.Sprintf("environments.%s.proxy", envName), true)
	viper.Set("environment", envName)

	if err := viper.WriteConfig(); err != nil {
		pterm.Error.Println("Failed to write configuration:", err)
		return
	}

	pterm.Success.Printf("Environment '%s' successfully initialized in '%s'.\n", envName, configPath)
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

	// Construct the endpoint
	newEndpoint := fmt.Sprintf("grpc+ssl://%s.api.%s.spaceone.dev:443", service, prefix)
	return newEndpoint, nil
}

func init() {
	// Adding subcommands to ConfigCmd
	ConfigCmd.AddCommand(configInitCmd)
	ConfigCmd.AddCommand(envCmd)
	ConfigCmd.AddCommand(showCmd)
	ConfigCmd.AddCommand(configEndpointCmd)
	configInitCmd.AddCommand(configInitURLCmd)
	configInitCmd.AddCommand(configInitLocalCmd)

	// Defining flags for configInitCmd
	configInitCmd.Flags().StringP("environment", "e", "", "Override environment name")
	configInitURLCmd.Flags().StringP("url", "u", "", "URL for the environment")
	configInitURLCmd.Flags().Bool("app", false, "Initialize as application configuration")
	configInitURLCmd.Flags().Bool("user", false, "Initialize as user-specific configuration")

	// Defining flags for envCmd
	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")
	envCmd.Flags().BoolP("list", "l", false, "List available environments")

	// Defining flags for showCmd
	showCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml/json)")

	// Add flags for configEndpointCmd
	configEndpointCmd.Flags().StringP("service", "s", "", "Service to set the endpoint for")

	viper.SetConfigType("yaml")
}
