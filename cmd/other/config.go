// config.go

package other

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jhump/protoreflect/dynamic"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
)

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

		// Create config directory if it doesn't exist
		configDir := GetConfigDir()
		if err := os.MkdirAll(configDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create config directory: %v\n", err)
			return
		}

		// Initialize config.yaml if it doesn't exist
		mainConfigPath := filepath.Join(configDir, "config.yaml")
		if _, err := os.Stat(mainConfigPath); os.IsNotExist(err) {
			initialConfig := []byte("environments:\n")
			if err := os.WriteFile(mainConfigPath, initialConfig, 0644); err != nil {
				pterm.Error.Printf("Failed to create config file: %v\n", err)
				return
			}
		}

		// Initialize the environment
		if appFlag {
			envName = fmt.Sprintf("%s-app", envName)
		} else {
			envName = fmt.Sprintf("%s-user", envName)
		}

		// Update configuration
		updateConfig(envName, urlStr, map[bool]string{true: "app", false: "user"}[appFlag])

		// Update the current environment in the main config
		mainV := viper.New()
		mainV.SetConfigFile(mainConfigPath)

		// Read the config file
		if err := mainV.ReadInConfig(); err != nil {
			pterm.Error.Printf("Failed to read config file: %v\n", err)
			return
		}

		// Set the new environment as current
		mainV.Set("environment", envName)

		if err := mainV.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to update current environment: %v\n", err)
			return
		}

		pterm.Success.Printf("Switched to '%s' environment.\n", envName)
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
		configDir := GetConfigDir()
		appConfigPath := filepath.Join(configDir, "config.yaml")
		userConfigPath := filepath.Join(configDir, "cache", "config.yaml")

		// Create separate Viper instances
		appV := viper.New()
		userV := viper.New()

		// Load app configuration
		if err := loadConfig(appV, appConfigPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Load user configuration
		if err := loadConfig(userV, userConfigPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Get current environment (from app config only)
		currentEnv := getCurrentEnvironment(appV)

		// Check if -s or -r flag is provided
		switchEnv, _ := cmd.Flags().GetString("switch")
		removeEnv, _ := cmd.Flags().GetString("remove")

		// Handle environment switching (app config only)
		if switchEnv != "" {
			// Check environment in both app and user configs
			appEnvMap := appV.GetStringMap("environments")
			userEnvMap := userV.GetStringMap("environments")

			if currentEnv == switchEnv {
				pterm.Info.Printf("Already in '%s' environment.\n", currentEnv)
				return
			}

			if _, existsApp := appEnvMap[switchEnv]; !existsApp {
				if _, existsUser := userEnvMap[switchEnv]; !existsUser {
					home, _ := os.UserHomeDir()
					pterm.Error.Printf("Environment '%s' not found in either %s/.cfctl/config.yaml or %s/.cfctl/cache/config.yaml\n",
						switchEnv, home, home)
					return
				}
			}

			// Update only the environment field in app config
			appV.Set("environment", switchEnv)

			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update environment in config.yaml: %v", err)
				return
			}

			pterm.Success.Printf("Switched to '%s' environment.\n", switchEnv)
			updateGlobalConfig()
			return
		}

		// Handle environment removal with confirmation
		if removeEnv != "" {
			// Determine which Viper instance contains the environment
			var targetViper *viper.Viper
			var targetConfigPath string
			envMapApp := appV.GetStringMap("environments")
			envMapUser := userV.GetStringMap("environments")

			if _, exists := envMapApp[removeEnv]; exists {
				targetViper = appV
				targetConfigPath = appConfigPath
			} else if _, exists := envMapUser[removeEnv]; exists {
				targetViper = userV
				targetConfigPath = userConfigPath
			} else {
				home, _ := os.UserHomeDir()
				pterm.Error.Printf("Environment '%s' not found in either %s/.cfctl/config.yaml or %s/.cfctl/cache/config.yaml\n",
					removeEnv, home, home)
				return
			}

			// Ask for confirmation before deletion
			fmt.Printf("Are you sure you want to delete the environment '%s'? (Y/N): ", removeEnv)
			var response string
			fmt.Scanln(&response)
			response = strings.ToLower(strings.TrimSpace(response))

			if response == "y" {
				// Remove the environment from the environments map
				envMap := targetViper.GetStringMap("environments")
				delete(envMap, removeEnv)
				targetViper.Set("environments", envMap)

				// Write the updated configuration back to the respective config file
				if err := targetViper.WriteConfig(); err != nil {
					pterm.Error.Printf("Failed to update config file '%s': %v", targetConfigPath, err)
					return
				}

				// If the deleted environment was the current one, unset it
				if currentEnv == removeEnv {
					appV.Set("environment", "")
					if err := appV.WriteConfig(); err != nil {
						pterm.Error.Printf("Failed to update environment in config.yaml: %v", err)
						return
					}
					pterm.Info.WithShowLineNumber(false).Println("Cleared current environment in config.yaml")
				}

				// Display success message
				pterm.Success.Printf("Removed '%s' environment from %s.\n", removeEnv, targetConfigPath)
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
			appEnvMap := appV.GetStringMap("environments")
			userEnvMap := userV.GetStringMap("environments")

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
					pterm.FgGreen.Printf("%s (current)\n", envName)
				} else {
					if _, isApp := appEnvMap[envName]; isApp {
						pterm.Printf("%s\n", envName)
					} else {
						pterm.Printf("%s\n", envName)
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
		configDir := GetConfigDir()
		appConfigPath := filepath.Join(configDir, "config.yaml")
		userConfigPath := filepath.Join(configDir, "cache", "config.yaml")

		// Create separate Viper instances
		appV := viper.New()
		userV := viper.New()

		// Load app configuration
		if err := loadConfig(appV, appConfigPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Load user configuration
		if err := loadConfig(userV, userConfigPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		currentEnv := getCurrentEnvironment(appV)
		if currentEnv == "" {
			pterm.Sprintf("No environment set in %s\n", appConfigPath)
			return
		}

		// Try to get the environment from appViper
		envConfig := appV.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))

		// If not found in appViper, try userViper
		if len(envConfig) == 0 {
			envConfig = userV.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))
			if len(envConfig) == 0 {
				pterm.Error.Printf("Environment '%s' not found in %s or %s\n", currentEnv, appConfigPath, userConfigPath)
				return
			}
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

Available Services are fetched dynamically from the backend.`,
	Run: func(cmd *cobra.Command, args []string) {
		service, _ := cmd.Flags().GetString("service")
		if service == "" {
			// Create a new Viper instance for app config
			appV := viper.New()

			// Load app configuration
			configPath := filepath.Join(GetConfigDir(), "config.yaml")
			if err := loadConfig(appV, configPath); err != nil {
				pterm.Error.Println(err)
				return
			}

			token, err := getToken(appV)
			if err != nil {
				pterm.Error.Println("Error retrieving token:", err)
				return
			}

			pterm.Error.Println("Please specify a service using -s or --service.")
			fmt.Println()

			// Fetch and display available services
			baseURL, err := getBaseURL(appV)
			if err != nil {
				pterm.Error.Println("Error retrieving base URL:", err)
				return
			}

			services, err := fetchAvailableServices(baseURL, token)
			if err != nil {
				pterm.Error.Println("Error fetching available services:", err)
				return
			}

			if len(services) == 0 {
				pterm.Println("No available services found.")
				return
			}

			var formattedServices []string
			for _, service := range services {
				if service == "identity" {
					formattedServices = append(formattedServices, pterm.FgCyan.Sprintf("%s (proxy)", service))
				} else {
					formattedServices = append(formattedServices, pterm.FgDefault.Sprint(service))
				}
			}

			pterm.DefaultBox.WithTitle("Available Services").
				WithRightPadding(1).
				WithLeftPadding(1).
				WithTopPadding(0).
				WithBottomPadding(0).
				Println(strings.Join(formattedServices, "\n"))
			return
		}

		// Create Viper instances for both app and cache configs
		appV := viper.New()
		cacheV := viper.New()

		// Load app configuration (for getting current environment)
		configPath := filepath.Join(GetConfigDir(), "config.yaml")
		if err := loadConfig(appV, configPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Load cache configuration
		cachePath := filepath.Join(GetConfigDir(), "cache", "config.yaml")
		if err := loadConfig(cacheV, cachePath); err != nil {
			pterm.Error.Println(err)
			return
		}

		currentEnv := getCurrentEnvironment(appV)
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

		// Update the appropriate config file based on environment type
		if strings.HasSuffix(currentEnv, "-app") {
			// Update endpoint in main config for app environments
			appV.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), newEndpoint)
			if service != "identity" {
				appV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), false)
			} else {
				appV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), true)
			}

			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update config.yaml: %v\n", err)
				return
			}
		} else {
			// Update endpoint in cache config for user environments
			cachePath := filepath.Join(GetConfigDir(), "cache", "config.yaml")
			if err := loadConfig(cacheV, cachePath); err != nil {
				pterm.Error.Println(err)
				return
			}

			cacheV.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), newEndpoint)
			if service != "identity" {
				cacheV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), false)
			} else {
				cacheV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), true)
			}

			if err := cacheV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update cache/config.yaml: %v\n", err)
				return
			}
		}

		pterm.Success.Printf("Updated endpoint for '%s' to '%s'.\n", currentEnv, newEndpoint)
	},
}

// fetchAvailableServices retrieves the list of services by calling the List method on the Endpoint service.
func fetchAvailableServices(endpoint, token string) ([]string, error) {
	if !strings.Contains(endpoint, "identity.api") {
		parts := strings.Split(endpoint, "://")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
		}

		hostParts := strings.Split(parts[1], ".")
		if len(hostParts) < 4 {
			return nil, fmt.Errorf("invalid endpoint format: %s", endpoint)
		}
		env := hostParts[2]

		endpoint = fmt.Sprintf("grpc+ssl://identity.api.%s.spaceone.dev:443", env)
	}

	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse endpoint: %w", err)
	}

	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "443" // Default gRPC port
	}

	var opts []grpc.DialOption

	// Set up TLS credentials if the scheme is grpc+ssl://
	if strings.HasPrefix(endpoint, "grpc+ssl://") {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false, // Set to true only if you want to skip TLS verification (not recommended)
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		return nil, fmt.Errorf("unsupported scheme in endpoint: %s", endpoint)
	}

	// Add token-based authentication if a token is provided
	if token != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(&tokenCreds{token}))
	}

	// Establish a connection to the gRPC server
	conn, err := grpc.Dial(fmt.Sprintf("%s:%s", host, port), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gRPC endpoint: %w", err)
	}
	defer conn.Close()

	ctx := context.Background()

	// Create a reflection client to discover services and methods
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// Resolve the service descriptor for "spaceone.api.identity.v2.Endpoint"
	serviceName := "spaceone.api.identity.v2.Endpoint"
	svcDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service %s: %w", serviceName, err)
	}

	// Resolve the method descriptor for the "List" method
	methodName := "list"
	methodDesc := svcDesc.FindMethodByName(methodName)
	if methodDesc == nil {
		return nil, fmt.Errorf("method '%s' not found in service '%s'", methodName, serviceName)
	}

	inputType := methodDesc.GetInputType()
	if inputType == nil {
		return nil, fmt.Errorf("input type not found for method '%s'", methodName)
	}

	// Get the request and response message descriptors
	reqDesc := methodDesc.GetInputType()
	respDesc := methodDesc.GetOutputType()

	// Create a dynamic message for the request
	reqMsg := dynamic.NewMessage(reqDesc)
	// If ListRequest has required fields, set them here. For example:
	// reqMsg.SetField("page_size", 100)

	// Create a dynamic message for the response
	respMsg := dynamic.NewMessage(respDesc)

	// Invoke the RPC method
	//err = grpc.Invoke(ctx, fmt.Sprintf("/%s/%s", serviceName, methodName), reqMsg, conn, respMsg)
	err = conn.Invoke(ctx, fmt.Sprintf("/%s/%s", serviceName, methodName), reqMsg, respMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke RPC: %w", err)
	}

	// Extract the 'results' field from the response message
	resultsFieldDesc := respDesc.FindFieldByName("results")
	if resultsFieldDesc == nil {
		return nil, fmt.Errorf("'results' field not found in response message")
	}

	resultsField, err := respMsg.TryGetField(resultsFieldDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to get 'results' field: %w", err)
	}

	// 'results' is expected to be a repeated field (list) of messages
	resultsSlice, ok := resultsField.([]interface{})
	if !ok {
		return nil, fmt.Errorf("'results' field is not a list")
	}

	var availableServices []string
	for _, res := range resultsSlice {
		// Each item in 'results' should be a dynamic.Message
		resMsg, ok := res.(*dynamic.Message)
		if !ok {
			continue
		}

		// Extract the 'service' field from each result message
		serviceFieldDesc := resMsg.GetMessageDescriptor().FindFieldByName("service")
		if serviceFieldDesc == nil {
			continue // Skip if 'service' field is not found
		}

		serviceField, err := resMsg.TryGetField(serviceFieldDesc)
		if err != nil {
			continue // Skip if unable to get the 'service' field
		}

		serviceStr, ok := serviceField.(string)
		if !ok {
			continue // Skip if 'service' field is not a string
		}

		availableServices = append(availableServices, serviceStr)
	}

	return availableServices, nil
}

// tokenCreds implements grpc.PerRPCCredentials for token-based authentication.
type tokenCreds struct {
	token string
}

func (t *tokenCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": fmt.Sprintf("Bearer %s", t.token),
	}, nil
}

func (t *tokenCreds) RequireTransportSecurity() bool {
	return true
}

// getBaseURL retrieves the base URL for the current environment from the given Viper instance.
func getBaseURL(v *viper.Viper) (string, error) {
	currentEnv := getCurrentEnvironment(v)
	if currentEnv == "" {
		return "", fmt.Errorf("no environment is set")
	}

	baseURL := v.GetString(fmt.Sprintf("environments.%s.endpoint", currentEnv))

	if baseURL == "" {
		cacheV := viper.New()
		cachePath := filepath.Join(GetConfigDir(), "cache", "config.yaml")

		if err := loadConfig(cacheV, cachePath); err != nil {
			return "", fmt.Errorf("failed to load cache config: %v", err)
		}

		baseURL = cacheV.GetString(fmt.Sprintf("environments.%s.endpoint", currentEnv))
	}

	if baseURL == "" {
		return "", fmt.Errorf("no endpoint found for environment '%s' in either config.yaml or cache/config.yaml", currentEnv)
	}

	return baseURL, nil
}

// getToken retrieves the token for the current environment.
func getToken(v *viper.Viper) (string, error) {
	home, _ := os.UserHomeDir()
	currentEnv := getCurrentEnvironment(v)
	if currentEnv == "" {
		return "", fmt.Errorf("no environment is set")
	}

	// Check if the environment is app or user type
	if strings.HasSuffix(currentEnv, "-app") {
		// For app environments, check only in main config
		token := v.GetString(fmt.Sprintf("environments.%s.token", currentEnv))
		if token == "" {
			return "", fmt.Errorf("no token found for app environment '%s' in %s/.cfctl/config.yaml", currentEnv, home)
		}
		return token, nil
	} else if strings.HasSuffix(currentEnv, "-user") {
		// For user environments, check only in cache config
		cacheV := viper.New()
		cachePath := filepath.Join(GetConfigDir(), "cache", "config.yaml")

		if err := loadConfig(cacheV, cachePath); err != nil {
			return "", fmt.Errorf("failed to load cache config: %v", err)
		}

		token := cacheV.GetString(fmt.Sprintf("environments.%s.token", currentEnv))
		if token == "" {
			return "", fmt.Errorf("no token found for user environment '%s' in %s", currentEnv, cachePath)
		}
		return token, nil
	}

	return "", fmt.Errorf("environment '%s' has invalid suffix (must end with -app or -user)", currentEnv)
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

	// Set the config type explicitly to YAML
	v.SetConfigType("yaml")

	// Check if the config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Initialize with default values
		v.Set("environments", map[string]interface{}{})
		v.Set("environment", "")

		// Convert to YAML with 2-space indentation
		config := map[string]interface{}{
			"environments": map[string]interface{}{},
			"environment":  "",
		}

		data, err := yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal config: %w", err)
		}

		// Write the default config to the file
		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return fmt.Errorf("failed to create config file '%s': %w", configPath, err)
		}
	}

	// Read the config file
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file '%s': %w", configPath, err)
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

// updateConfig updates the configuration files based on the environment type
func updateConfig(envName, urlStr, configType string) {
	configDir := GetConfigDir()
	mainConfigPath := filepath.Join(configDir, "config.yaml")

	// Initialize main viper instance
	mainV := viper.New()
	mainV.SetConfigFile(mainConfigPath)

	if err := mainV.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			pterm.Error.Printf("Error reading config file: %v\n", err)
			return
		}
	}

	// Handle app type configuration
	if configType == "app" && urlStr != "" {
		endpoint, err := constructEndpoint(urlStr)
		if err != nil {
			pterm.Error.Printf("Failed to construct endpoint: %v\n", err)
			return
		}

		mainV.Set(fmt.Sprintf("environments.%s.endpoint", envName), endpoint)
		mainV.Set(fmt.Sprintf("environments.%s.proxy", envName), true)
		mainV.Set(fmt.Sprintf("environments.%s.token", envName), "")

		if err := mainV.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to write config: %v\n", err)
			return
		}
	}

	// Handle user type configuration
	if configType == "user" {
		cacheDir := filepath.Join(configDir, "cache")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create cache directory: %v\n", err)
			return
		}

		cacheConfigPath := filepath.Join(cacheDir, "config.yaml")

		// Create cache config file if it doesn't exist
		if _, err := os.Stat(cacheConfigPath); os.IsNotExist(err) {
			initialConfig := []byte("environments:\n")
			if err := os.WriteFile(cacheConfigPath, initialConfig, 0644); err != nil {
				pterm.Error.Printf("Failed to create cache config file: %v\n", err)
				return
			}
		}

		cacheV := viper.New()
		cacheV.SetConfigFile(cacheConfigPath)

		if err := cacheV.ReadInConfig(); err != nil {
			if !os.IsNotExist(err) {
				pterm.Error.Printf("Error reading cache config: %v\n", err)
				return
			}
		}

		if urlStr != "" {
			endpoint, err := constructEndpoint(urlStr)
			if err != nil {
				pterm.Error.Printf("Failed to construct endpoint: %v\n", err)
				return
			}

			cacheV.Set(fmt.Sprintf("environments.%s.endpoint", envName), endpoint)
			cacheV.Set(fmt.Sprintf("environments.%s.proxy", envName), true)
			cacheV.Set(fmt.Sprintf("environments.%s.token", envName), "")
		}

		if err := cacheV.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to write cache config: %v\n", err)
			return
		}
	}

	pterm.Success.Printf("Environment '%s' successfully initialized.\n", envName)
}

// convertToStringMap converts map[interface{}]interface{} to map[string]interface{}
func convertToStringMap(m map[interface{}]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		switch v := v.(type) {
		case map[interface{}]interface{}:
			result[k.(string)] = convertToStringMap(v)
		case []interface{}:
			result[k.(string)] = convertToSlice(v)
		default:
			result[k.(string)] = v
		}
	}
	return result
}

// convertToSlice handles slice conversion if needed
func convertToSlice(s []interface{}) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		switch v := v.(type) {
		case map[interface{}]interface{}:
			result[i] = convertToStringMap(v)
		case []interface{}:
			result[i] = convertToSlice(v)
		default:
			result[i] = v
		}
	}
	return result
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
	// TODO: After set up production
	default:
		return "", fmt.Errorf("unknown environment prefix in URL: %s", hostname)
	}

	// Extract the service from the hostname
	service := strings.Split(hostname, ".")[0]
	if service == "" {
		return "", fmt.Errorf("unable to determine service from URL: %s", hostname)
	}

	// Construct the endpoint dynamically based on the service
	newEndpoint := fmt.Sprintf("grpc+ssl://identity.api.%s.spaceone.dev:443", prefix)
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
