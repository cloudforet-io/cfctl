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

	"github.com/pelletier/go-toml/v2"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// SettingCmd represents the setting command
var SettingCmd = &cobra.Command{
	Use:   "setting",
	Short: "Manage cfctl setting file",
	Long: `Manage setting file for cfctl. You can initialize,
switch environments, and display the current configuration.`,
}

// settingInitCmd initializes a new environment configuration
var settingInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new environment setting",
	Long:  `Initialize a new environment setting for cfctl by specifying either a URL or a local environment name.`,
}

// settingInitURLCmd initializes configuration with a URL
var settingInitURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Initialize configuration with a URL",
	Long:  `Specify a URL to initialize the environment configuration.`,
	Args:  cobra.NoArgs,
	Example: `  cfctl setting init url -u https://example.com --app
                          or
  cfctl setting init url -u https://example.com --user`,
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

		// Create setting directory if it doesn't exist
		settingDir := GetSettingDir()
		if err := os.MkdirAll(settingDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create setting directory: %v\n", err)
			return
		}

		// Initialize setting.toml if it doesn't exist
		mainSettingPath := filepath.Join(settingDir, "setting.toml")
		if _, err := os.Stat(mainSettingPath); os.IsNotExist(err) {
			// Initial TOML structure
			initialSetting := []byte("environments = {}\n")
			if err := os.WriteFile(mainSettingPath, initialSetting, 0644); err != nil {
				pterm.Error.Printf("Failed to create setting file: %v\n", err)
				return
			}
		}

		// Update configuration in main setting file
		updateSetting(envName, urlStr, map[bool]string{true: "app", false: "user"}[appFlag])

		// Update the current environment
		mainV := viper.New()
		mainV.SetConfigFile(mainSettingPath)
		mainV.SetConfigType("toml")

		if err := mainV.ReadInConfig(); err != nil && !os.IsNotExist(err) {
			pterm.Error.Printf("Failed to read setting file: %v\n", err)
			return
		}

		// Set the environment name with app/user suffix
		envName = fmt.Sprintf("%s-%s", envName, map[bool]string{true: "app", false: "user"}[appFlag])
		mainV.Set("environment", envName)

		if err := mainV.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to update current environment: %v\n", err)
			return
		}

		pterm.Success.Printf("Switched to '%s' environment.\n", envName)
	},
}

// settingInitLocalCmd initializes configuration with a local environment
var settingInitLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Initialize configuration with a local environment",
	Long:  `Specify a local environment name to initialize the configuration.`,
	Args:  cobra.NoArgs,
	Example: `  cfctl setting init local -n [domain] --app --dev
                            or
  cfctl setting init local -n [domain] --user --stg`,
	Run: func(cmd *cobra.Command, args []string) {
		localEnv, _ := cmd.Flags().GetString("name")
		appFlag, _ := cmd.Flags().GetBool("app")
		userFlag, _ := cmd.Flags().GetBool("user")
		devFlag, _ := cmd.Flags().GetBool("dev")
		stgFlag, _ := cmd.Flags().GetBool("stg")

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
		if !devFlag && !stgFlag {
			pterm.Error.Println("You must specify either --dev or --stg flag.")
			cmd.Help()
			return
		}

		// Create setting directory if it doesn't exist
		settingDir := GetSettingDir()
		if err := os.MkdirAll(settingDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create setting directory: %v\n", err)
			return
		}

		// Initialize setting.toml if it doesn't exist
		mainSettingPath := filepath.Join(settingDir, "setting.toml")
		if _, err := os.Stat(mainSettingPath); os.IsNotExist(err) {
			// Initial TOML structure
			initialSetting := []byte("environments = {}\n")
			if err := os.WriteFile(mainSettingPath, initialSetting, 0644); err != nil {
				pterm.Error.Printf("Failed to create setting file: %v\n", err)
				return
			}
		}

		envPrefix := ""
		if devFlag {
			envPrefix = "dev"
		} else if stgFlag {
			envPrefix = "stg"
		}

		var envName string
		if appFlag {
			envName = fmt.Sprintf("local-%s-%s-app", envPrefix, localEnv)
		} else {
			envName = fmt.Sprintf("local-%s-%s-user", envPrefix, localEnv)
		}

		if appFlag {
			updateLocalSetting(envName, "app", mainSettingPath)
		} else {
			updateLocalSetting(envName, "user", mainSettingPath)
		}

		// Update the current environment in the main setting
		mainV := viper.New()
		mainV.SetConfigFile(mainSettingPath)
		mainV.SetConfigType("toml")

		// Read the setting file
		if err := mainV.ReadInConfig(); err != nil {
			pterm.Error.Printf("Failed to read setting file: %v\n", err)
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

func updateLocalSetting(envName, settingType, settingPath string) {
	v := viper.New()
	v.SetConfigFile(settingPath)

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(settingPath), 0755); err != nil {
		pterm.Error.Printf("Failed to create directory: %v\n", err)
		return
	}

	// Read existing setting or create new
	if err := v.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		pterm.Error.Printf("Error reading setting: %v\n", err)
		return
	}

	// Set environment configuration
	v.Set(fmt.Sprintf("environments.%s.endpoint", envName), "grpc://localhost:50051")
	if settingType == "app" {
		v.Set(fmt.Sprintf("environments.%s.token", envName), "")
		v.Set(fmt.Sprintf("environments.%s.proxy", envName), true)
	} else if settingType == "user" {
		v.Set(fmt.Sprintf("environments.%s.proxy", envName), false)
	}

	// Write configuration
	if err := v.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to write setting: %v\n", err)
		return
	}
}

// envCmd manages environment switching and listing
var envCmd = &cobra.Command{
	Use:   "environment",
	Short: "List and manage environments",
	Long:  "List and manage environments",
	Run: func(cmd *cobra.Command, args []string) {
		// Set paths for app and user configurations
		settingDir := GetSettingDir()
		appSettingPath := filepath.Join(settingDir, "setting.toml")

		// Create separate Viper instances
		appV := viper.New()

		// Load app configuration
		if err := loadSetting(appV, appSettingPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		// Get current environment (from app setting only)
		currentEnv := getCurrentEnvironment(appV)

		// Check if -s or -r flag is provided
		switchEnv, _ := cmd.Flags().GetString("switch")
		removeEnv, _ := cmd.Flags().GetString("remove")

		// Handle environment switching (app setting only)
		if switchEnv != "" {
			// Check environment in both app and user settings
			appEnvMap := appV.GetStringMap("environments")

			if currentEnv == switchEnv {
				pterm.Info.Printf("Already in '%s' environment.\n", currentEnv)
				return
			}

			if _, existsApp := appEnvMap[switchEnv]; !existsApp {
				home, _ := os.UserHomeDir()
				pterm.Error.Printf("Environment '%s' not found in %s/.cfctl/setting.toml",
					switchEnv, home)
				return
			}

			// Update only the environment field in app setting
			appV.Set("environment", switchEnv)

			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update environment in setting.toml: %v", err)
				return
			}

			pterm.Success.Printf("Switched to '%s' environment.\n", switchEnv)
			updateGlobalSetting()
			return
		}

		// Handle environment removal with confirmation
		if removeEnv != "" {
			// Determine which Viper instance contains the environment
			var targetViper *viper.Viper
			var targetSettingPath string
			envMapApp := appV.GetStringMap("environments")

			if _, exists := envMapApp[removeEnv]; exists {
				targetViper = appV
				targetSettingPath = appSettingPath
			} else {
				home, _ := os.UserHomeDir()
				pterm.Error.Printf("Environment '%s' not found in %s/.cfctl/setting.toml",
					switchEnv, home)
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

				// Write the updated configuration back to the respective setting file
				if err := targetViper.WriteConfig(); err != nil {
					pterm.Error.Printf("Failed to update setting file '%s': %v", targetSettingPath, err)
					return
				}

				// If the deleted environment was the current one, unset it
				if currentEnv == removeEnv {
					appV.Set("environment", "")
					if err := appV.WriteConfig(); err != nil {
						pterm.Error.Printf("Failed to update environment in setting.toml: %v", err)
						return
					}
					pterm.Info.WithShowLineNumber(false).Println("Cleared current environment in setting.toml")
				}

				// Display success message
				pterm.Success.Printf("Removed '%s' environment from %s.\n", removeEnv, targetSettingPath)
			} else {
				pterm.Info.Println("Environment deletion canceled.")
			}
			return
		}

		// Check if the -l flag is provided
		listOnly, _ := cmd.Flags().GetBool("list")

		// List environments if the -l flag is set
		if listOnly {
			// Get environment maps from both app and user settings
			appEnvMap := appV.GetStringMap("environments")

			// Map to store all unique environments
			allEnvs := make(map[string]bool)

			// Add app environments
			for envName := range appEnvMap {
				allEnvs[envName] = true
			}

			if len(allEnvs) == 0 {
				pterm.Println("No environments found in setting file")
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
		settingDir := GetSettingDir()
		appSettingPath := filepath.Join(settingDir, "setting.toml")
		userSettingPath := filepath.Join(settingDir, "cache", "setting.toml")

		// Create separate Viper instances
		appV := viper.New()
		userV := viper.New()

		// Load app configuration
		if err := loadSetting(appV, appSettingPath); err != nil {
			pterm.Error.Println(err)
			return
		}

		currentEnv := getCurrentEnvironment(appV)
		if currentEnv == "" {
			pterm.Sprintf("No environment set in %s\n", appSettingPath)
			return
		}

		// Try to get the environment from appViper
		envSetting := appV.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))

		// If not found in appViper, try userViper
		if len(envSetting) == 0 {
			envSetting = userV.GetStringMap(fmt.Sprintf("environments.%s", currentEnv))
			if len(envSetting) == 0 {
				pterm.Error.Printf("Environment '%s' not found in %s or %s\n", currentEnv, appSettingPath, userSettingPath)
				return
			}
		}

		output, _ := cmd.Flags().GetString("output")

		switch output {
		case "json":
			data, err := json.MarshalIndent(envSetting, "", "  ")
			if err != nil {
				log.Fatalf("Error formatting output as JSON: %v", err)
			}
			fmt.Println(string(data))
		case "toml":
			data, err := toml.Marshal(envSetting)
			if err != nil {
				log.Fatalf("Error formatting output as TOML: %v", err)
			}
			fmt.Println(string(data))
		default:
			log.Fatalf("Unsupported output format: %v", output)
		}
	},
}

// settingEndpointCmd updates the endpoint for the current environment
var settingEndpointCmd = &cobra.Command{
	Use:   "endpoint",
	Short: "Set the endpoint for the current environment",
	Long: `Update the endpoint for the current environment based on the specified service.
If the service is not 'identity', the proxy setting will be updated to false.

Available Services are fetched dynamically from the backend.`,
	Run: func(cmd *cobra.Command, args []string) {
		service, _ := cmd.Flags().GetString("service")
		if service == "" {
			// Create a new Viper instance for app setting
			appV := viper.New()

			// Load app configuration
			settingPath := filepath.Join(GetSettingDir(), "setting.toml")
			appV.SetConfigFile(settingPath)
			appV.SetConfigType("toml")

			if err := loadSetting(appV, settingPath); err != nil {
				pterm.Error.Println(err)
				return
			}

			token, err := getToken(appV)
			if err != nil {
				currentEnv := getCurrentEnvironment(appV)
				if strings.HasSuffix(currentEnv, "-app") {
					// Parse environment name to extract service name and environment
					parts := strings.Split(currentEnv, "-")
					if len(parts) >= 3 {
						envPrefix := parts[0]   // dev, stg
						serviceName := parts[1] // cloudone, spaceone, etc.
						url := fmt.Sprintf("https://%s.console.%s.spaceone.dev", serviceName, envPrefix)
						settingPath := filepath.Join(GetSettingDir(), "setting.toml")

						// Create header for the error message
						//pterm.DefaultHeader.WithBackgroundStyle(pterm.NewStyle(pterm.BgRed)).WithMargin(10).Println("Token Not Found")
						pterm.DefaultBox.
							WithTitle("Token Not Found").
							WithTitleTopCenter().
							WithBoxStyle(pterm.NewStyle(pterm.FgWhite)).
							WithRightPadding(1).
							WithLeftPadding(1).
							WithTopPadding(0).
							WithBottomPadding(0).
							Println("Please follow the instructions below to obtain an App Token.")

						// Create a styled box with instructions
						boxContent := fmt.Sprintf(`Please follow these steps to obtain an App Token:

1. Visit %s
2. Go to Admin page or Workspace page
3. Navigate to the App page
4. Click [Create] button
5. Copy the generated App Token
6. Update your settings:
     Path: %s
     Environment: %s
     Field: "token"`,
							pterm.FgLightCyan.Sprint(url),
							pterm.FgLightYellow.Sprint(settingPath),
							pterm.FgLightGreen.Sprint(currentEnv))

						// Print the box with instructions
						pterm.DefaultBox.
							WithTitle("Setup Instructions").
							WithTitleTopCenter().
							WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).
							// WithTextAlignment(pterm.TextAlignLeft).
							Println(boxContent)

						// Print additional help message
						pterm.Info.Println("After updating the token, please try your command again.")

						return
					}
				} else if strings.HasSuffix(currentEnv, "-user") {
					pterm.Error.Printf("No token found for environment '%s'. Please run 'cfctl login' to authenticate.\n", currentEnv)
				} else {
					pterm.Error.Println("Error retrieving token:", err)
				}
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

		// Create Viper instances for both app and cache settings
		appV := viper.New()
		cacheV := viper.New()

		// Load app configuration (for getting current environment)
		settingPath := filepath.Join(GetSettingDir(), "setting.toml")
		appV.SetConfigFile(settingPath)
		appV.SetConfigType("toml")

		// Load cache configuration
		cachePath := filepath.Join(GetSettingDir(), "cache", "setting.toml")
		cacheV.SetConfigFile(cachePath)
		cacheV.SetConfigType("toml")

		if err := loadSetting(appV, settingPath); err != nil {
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

		// Update the appropriate setting file based on environment type
		if strings.HasSuffix(currentEnv, "-app") {
			// Update endpoint in main setting for app environments
			appV.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), newEndpoint)
			if service != "identity" {
				appV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), false)
			} else {
				appV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), true)
			}

			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update setting.toml: %v\n", err)
				return
			}
		} else {
			// Update endpoint in cache setting for user environments
			cachePath := filepath.Join(GetSettingDir(), "cache", "setting.toml")
			if err := loadSetting(cacheV, cachePath); err != nil {
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
				pterm.Error.Printf("Failed to update cache/setting.toml: %v\n", err)
				return
			}
		}

		pterm.Success.Printf("Updated endpoint for '%s' to '%s'.\n", currentEnv, newEndpoint)
	},
}

// settingTokenCmd updates the token for the current environment
var settingTokenCmd = &cobra.Command{
	Use:   "token [token_value]",
	Short: "Set the token for the current environment",
	Long: `Update the token for the current environment.
This command only works with app environments (-app suffix).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Load current environment configuration file
		settingDir := GetSettingDir()
		settingPath := filepath.Join(settingDir, "setting.toml")

		v := viper.New()
		v.SetConfigFile(settingPath)
		v.SetConfigType("toml")

		if err := v.ReadInConfig(); err != nil {
			pterm.Error.Printf("Failed to read setting file: %v\n", err)
			return
		}

		// Get current environment
		currentEnv := v.GetString("environment")
		if currentEnv == "" {
			pterm.Error.Println("No environment is currently selected.")
			return
		}

		// Check if it's an app environment
		if !strings.HasSuffix(currentEnv, "-app") {
			pterm.Error.Println("Token can only be set for app environments (-app suffix).")
			return
		}

		// Update token
		tokenKey := fmt.Sprintf("environments.%s.token", currentEnv)
		v.Set(tokenKey, args[0])

		// Save configuration
		if err := v.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to update token: %v\n", err)
			return
		}

		pterm.Success.Printf("Token updated for '%s' environment.\n", currentEnv)
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
		tlsSetting := &tls.Config{
			InsecureSkipVerify: false, // Set to true only if you want to skip TLS verification (not recommended)
		}
		creds := credentials.NewTLS(tlsSetting)
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
		return "", fmt.Errorf("no endpoint found for environment '%s' in setting.toml", currentEnv)

	}

	return baseURL, nil
}

// getToken retrieves the token for the current environment.
func getToken(v *viper.Viper) (string, error) {
	currentEnv := getCurrentEnvironment(v)
	if currentEnv == "" {
		return "", fmt.Errorf("no environment is set")
	}

	token := v.GetString(fmt.Sprintf("environments.%s.token", currentEnv))
	if token == "" {
		return "", fmt.Errorf("no token found for environment '%s'", currentEnv)
	}
	return token, nil
}

// GetSettingDir returns the directory where setting file are stored
func GetSettingDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	return filepath.Join(home, ".cfctl")
}

// loadSetting ensures that the setting directory and setting file exist.
// It initializes the setting file with default values if it does not exist.
func loadSetting(v *viper.Viper, settingPath string) error {
	// Ensure the setting directory exists
	settingDir := filepath.Dir(settingPath)
	if err := os.MkdirAll(settingDir, 0755); err != nil {
		return fmt.Errorf("failed to create setting directory '%s': %w", settingDir, err)
	}

	// Set the setting file
	v.SetConfigFile(settingPath)
	v.SetConfigType("toml")

	// Read the setting file
	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			// Initialize with default values if file doesn't exist
			defaultSettings := map[string]interface{}{
				"environments": map[string]interface{}{},
				"environment":  "",
			}

			// Convert to TOML
			data, err := toml.Marshal(defaultSettings)
			if err != nil {
				return fmt.Errorf("failed to marshal default settings: %w", err)
			}

			// Write the default settings to file
			if err := os.WriteFile(settingPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write default settings: %w", err)
			}

			// Read the newly created file
			if err := v.ReadInConfig(); err != nil {
				return fmt.Errorf("failed to read newly created setting file: %w", err)
			}
		} else {
			return fmt.Errorf("failed to read setting file: %w", err)
		}
	}

	return nil
}

// getCurrentEnvironment reads the current environment from the given Viper instance
func getCurrentEnvironment(v *viper.Viper) string {
	return v.GetString("environment")
}

// updateGlobalSetting prints a success message for global setting update
func updateGlobalSetting() {
	settingPath := filepath.Join(GetSettingDir(), "setting.toml")
	v := viper.New()

	v.SetConfigFile(settingPath)

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			pterm.Success.WithShowLineNumber(false).Printfln("Global setting updated with existing environments. (default: %s/setting.toml)", GetSettingDir())
			return
		}
		pterm.Warning.Printf("Warning: Could not read global setting: %v\n", err)
		return
	}

	pterm.Success.WithShowLineNumber(false).Printfln("Global setting updated with existing environments. (default: %s/setting.toml)", GetSettingDir())
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

// updateSetting updates the configuration files
func updateSetting(envName, urlStr, settingType string) {
	settingDir := GetSettingDir()
	mainSettingPath := filepath.Join(settingDir, "setting.toml")

	// Initialize main viper instance
	mainV := viper.New()
	mainV.SetConfigFile(mainSettingPath)
	mainV.SetConfigType("toml")

	// Read existing configuration file
	if err := mainV.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			pterm.Error.Printf("Error reading setting file: %v\n", err)
			return
		}
	}

	// Initialize environments if not exists
	if !mainV.IsSet("environments") {
		mainV.Set("environments", make(map[string]interface{}))
	}

	if urlStr != "" {
		endpoint, err := constructEndpoint(urlStr)
		if err != nil {
			pterm.Error.Printf("Failed to construct endpoint: %v\n", err)
			return
		}

		// Append -app or -user to the environment name
		envName = fmt.Sprintf("%s-%s", envName, settingType)

		// Get environments map
		environments := mainV.GetStringMap("environments")
		if environments == nil {
			environments = make(map[string]interface{})
		}

		// Add new environment configuration
		envConfig := map[string]interface{}{
			"endpoint": endpoint,
			"proxy":    true,
		}

		// Only add token field for app configuration
		if settingType == "app" {
			envConfig["token"] = ""
		}

		environments[envName] = envConfig

		// Update entire configuration
		mainV.Set("environments", environments)
		mainV.Set("environment", envName)

		// Save configuration file
		if err := mainV.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to write setting: %v\n", err)
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
	SettingCmd.AddCommand(settingInitCmd)
	SettingCmd.AddCommand(envCmd)
	SettingCmd.AddCommand(showCmd)
	SettingCmd.AddCommand(settingEndpointCmd)
	SettingCmd.AddCommand(settingTokenCmd)
	settingInitCmd.AddCommand(settingInitURLCmd)
	settingInitCmd.AddCommand(settingInitLocalCmd)

	settingInitCmd.Flags().StringP("environment", "e", "", "Override environment name")

	settingInitURLCmd.Flags().StringP("url", "u", "", "URL for the environment")
	settingInitURLCmd.Flags().Bool("app", false, "Initialize as application configuration")
	settingInitURLCmd.Flags().Bool("user", false, "Initialize as user-specific configuration")

	settingInitLocalCmd.Flags().StringP("name", "n", "", "Local environment name for the environment")
	settingInitLocalCmd.Flags().Bool("app", false, "Initialize as application configuration")
	settingInitLocalCmd.Flags().Bool("user", false, "Initialize as user-specific configuration")
	settingInitLocalCmd.Flags().Bool("dev", false, "Initialize as development environment")
	settingInitLocalCmd.Flags().Bool("stg", false, "Initialize as staging environment")

	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")
	envCmd.Flags().BoolP("list", "l", false, "List available environments")

	showCmd.Flags().StringP("output", "o", "toml", "Output format (toml/json)")

	settingEndpointCmd.Flags().StringP("service", "s", "", "Service to set the endpoint for")

	// No need to set global Viper setting type since we are using separate instances
}
