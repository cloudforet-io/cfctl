package other

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudforet-io/cfctl/pkg/transport"
	"gopkg.in/yaml.v3"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type ServiceEndpoint struct {
	Name     string `json:"name"`
	Service  string `json:"service"`
	Endpoint string `json:"endpoint"`
}

// SettingCmd represents the setting command
var SettingCmd = &cobra.Command{
	Use:   "setting",
	Short: "Manage cfctl setting file",
	Long: `Manage setting file for cfctl. 
You can initialize, switch environments, and display the current configuration.`,
}

// settingInitCmd initializes a new environment configuration
var settingInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new environment setting",
	Long:  `Initialize a new environment setting for cfctl by specifying an endpoint`,
	Run: func(cmd *cobra.Command, args []string) {
		proxyFlag, _ := cmd.Flags().GetBool("proxy")
		staticFlag, _ := cmd.Flags().GetBool("static")

		if !proxyFlag && !staticFlag {
			pterm.Error.Println("You must specify either 'proxy' or 'static' command.")
			cmd.Help()
			return
		}
	},
}

// settingInitStaticCmd represents the setting init direct command
var settingInitStaticCmd = &cobra.Command{
	Use:   "static [endpoint]",
	Short: "Initialize static connection to a local or service endpoint",
	Long: `Initialize configuration with a static service endpoint.
This is useful for development or when connecting directly to specific service endpoints.`,
	Example: `  cfctl setting init static grpc://localhost:50051
  cfctl setting init static grpc[+ssl]://inventory-`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Get environment name from user input
		result, err := pterm.DefaultInteractiveTextInput.
			WithDefaultText("default").
			WithDefaultValue("default").
			WithMultiLine(false).
			Show("Environment name")

		if err != nil {
			pterm.Error.Printf("Failed to get environment name: %v\n", err)
			return
		}

		// If user didn't input anything, use default
		envName := result
		if envName == "" || envName == "default" {
			envName = "default"
		}

		endpoint := args[0]
		settingDir := GetSettingDir()
		if err := os.MkdirAll(settingDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create setting directory: %v\n", err)
			return
		}

		mainSettingPath := filepath.Join(settingDir, "setting.yaml")
		v := viper.New()
		v.SetConfigFile(mainSettingPath)
		v.SetConfigType("yaml")

		//envName, err := parseEnvNameFromURL(endpoint)
		//if err != nil {
		//	pterm.Error.Printf("Failed to parse environment name: %v\n", err)
		//	return
		//}

		// Check if environment already exists
		if err := v.ReadInConfig(); err == nil {
			environments := v.GetStringMap("environments")
			if existingEnv, exists := environments[envName]; exists {
				currentConfig, _ := yaml.Marshal(map[string]interface{}{
					"environment": envName,
					"environments": map[string]interface{}{
						envName: existingEnv,
					},
				})

				confirmBox := pterm.DefaultBox.WithTitle("Environment Already Exists").
					WithTitleTopCenter().
					WithRightPadding(4).
					WithLeftPadding(4).
					WithBoxStyle(pterm.NewStyle(pterm.FgYellow))

				confirmBox.Println(fmt.Sprintf("Environment '%s' already exists.\nDo you want to overwrite it?", envName))

				pterm.Info.Println("Current configuration:")
				fmt.Println(string(currentConfig))

				fmt.Print("\nEnter (y/n): ")
				var response string
				fmt.Scanln(&response)
				response = strings.ToLower(strings.TrimSpace(response))

				if response != "y" {
					pterm.Info.Printf("Operation cancelled. Environment '%s' remains unchanged.\n", envName)
					return
				}
			}
		}

		pterm.Success.Printf("Successfully initialized direct connection to %s\n", endpoint)
		updateSetting(envName, endpoint, "")
		if err := v.ReadInConfig(); err == nil {
			v.Set(fmt.Sprintf("environments.%s.proxy", envName), false)
			if err := v.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update proxy setting: %v\n", err)
				return
			}
		}
	},
}

// settingInitProxyCmd represents the setting init proxy command
var settingInitProxyCmd = &cobra.Command{
	Use:   "proxy [URL]",
	Short: "Initialize configuration with a proxy URL",
	Long:  `Specify a proxy URL to initialize the environment configuration.`,
	Args:  cobra.ExactArgs(1),
	Example: `  cfctl setting init proxy http[s]://example.com --app
  cfctl setting init proxy http[s]://example.com --user`,
	Run: func(cmd *cobra.Command, args []string) {
		endpointStr := args[0]
		appFlag, _ := cmd.Flags().GetBool("app")
		userFlag, _ := cmd.Flags().GetBool("user")

		if !appFlag && !userFlag {
			pterm.Error.Println("You must specify either --app or --user flag.")
			cmd.Help()
			return
		}

		// Get environment name from user input
		result, err := pterm.DefaultInteractiveTextInput.
			WithDefaultText("default").
			WithDefaultValue("default").
			WithMultiLine(false).
			Show("Environment name")

		if err != nil {
			pterm.Error.Printf("Failed to get environment name: %v\n", err)
			return
		}

		// If user didn't input anything, use default
		envPrefix := result
		if envPrefix == "" || envPrefix == "default" {
			envPrefix = "default"
		}

		// Add suffix based on flag
		var envName string
		if appFlag {
			envName = envPrefix + "-app"
		} else if userFlag {
			envName = envPrefix + "-user"
		}

		var envSuffix string
		if userFlag {
			envSuffix = "user"
		} else if appFlag {
			envSuffix = "app"
		}

		settingDir := GetSettingDir()
		if err := os.MkdirAll(settingDir, 0755); err != nil {
			pterm.Error.Printf("Failed to create setting directory: %v\n", err)
			return
		}

		mainSettingPath := filepath.Join(settingDir, "setting.yaml")
		v := viper.New()
		v.SetConfigFile(mainSettingPath)
		v.SetConfigType("yaml")

		// Always set proxy to true
		pterm.Success.Printf("Successfully initialized proxy connection to %s\n", endpointStr)

		if err := v.ReadInConfig(); err == nil {
			environments := v.GetStringMap("environments")
			if existingEnv, exists := environments[envName]; exists {
				currentConfig, _ := yaml.Marshal(map[string]interface{}{
					"environment": envName,
					"environments": map[string]interface{}{
						envName: existingEnv,
					},
				})

				confirmBox := pterm.DefaultBox.WithTitle("Environment Already Exists").
					WithTitleTopCenter().
					WithRightPadding(4).
					WithLeftPadding(4).
					WithBoxStyle(pterm.NewStyle(pterm.FgYellow))

				confirmBox.Println(fmt.Sprintf("Environment '%s' already exists.\nDo you want to overwrite it?", envName))

				pterm.Info.Println("Current configuration:")
				fmt.Println(string(currentConfig))

				fmt.Print("\nEnter (y/n): ")
				var response string
				fmt.Scanln(&response)
				response = strings.ToLower(strings.TrimSpace(response))

				if response != "y" {
					pterm.Info.Printf("Operation cancelled. Environment '%s' remains unchanged.\n", envName)
					return
				}
			}
		}

		// Update configuration
		updateSetting(envName, endpointStr, envSuffix)
	},
}

// envCmd manages environment switching and listing
var envCmd = &cobra.Command{
	Use:   "environment",
	Short: "List and manage environments",
	Long:  "List and manage environments",
	Run: func(cmd *cobra.Command, args []string) {
		// Set paths for app and user configurations
		settingDir := GetSettingDir()
		appSettingPath := filepath.Join(settingDir, "setting.yaml")

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
				pterm.Error.Printf("Environment '%s' not found in %s/.cfctl/setting.yaml",
					switchEnv, home)
				return
			}

			// Update only the environment field in app setting
			appV.Set("environment", switchEnv)

			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update environment in setting.yaml: %v", err)
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
				pterm.Error.Printf("Environment '%s' not found in %s/.cfctl/setting.yaml",
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
						pterm.Error.Printf("Failed to update environment in setting.yaml: %v", err)
						return
					}
					pterm.Info.WithShowLineNumber(false).Println("Cleared current environment in setting.yaml")
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

			tableData := pterm.TableData{
				{"Environment", "Type", "Endpoint", "Proxy", "Current"},
			}

			var envNames []string
			for envName := range allEnvs {
				envNames = append(envNames, envName)
			}
			sort.Strings(envNames)

			for _, envName := range envNames {
				envConfig := appV.GetStringMapString(fmt.Sprintf("environments.%s", envName))

				var envType string
				if strings.HasSuffix(envName, "-user") {
					envType = "User"
				} else if strings.HasSuffix(envName, "-app") {
					envType = "App"
				} else {
					envType = "Static"
				}

				endpoint := envConfig["endpoint"]

				proxyEnabled := appV.GetBool(fmt.Sprintf("environments.%s.proxy", envName))
				proxyStatus := ""
				if proxyEnabled {
					proxyStatus = pterm.Sprint("enabled")
				} else {
					proxyStatus = pterm.Sprint("disabled")
				}

				if envName == currentEnv {
					proxyText := "enabled"
					if !proxyEnabled {
						proxyText = "disabled"
					}

					tableData = append(tableData, []string{
						pterm.FgYellow.Sprint(envName),
						pterm.FgYellow.Sprint(envType),
						pterm.FgYellow.Sprint(endpoint),
						pterm.FgYellow.Sprint(proxyText),
						"   " + pterm.FgYellow.Sprint("✓") + "   ",
					})
				} else {
					tableData = append(tableData, []string{
						envName,
						envType,
						endpoint,
						proxyStatus,
						"       ",
					})
				}
			}

			pterm.Info.Println("Available Environments")

			pterm.DefaultTable.
				WithHasHeader().
				WithData(tableData).
				WithBoxed(true).
				WithHeaderStyle(pterm.NewStyle(pterm.FgLightCyan)).
				Render()

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
		appSettingPath := filepath.Join(settingDir, "setting.yaml")
		userSettingPath := filepath.Join(settingDir, "cache", "setting.yaml")

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
		case "yaml":
			data, err := yaml.Marshal(envSetting)
			if err != nil {
				log.Fatalf("Error formatting output as yaml: %v", err)
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
	Long: `Update the endpoint for the current environment.
You can either specify a new endpoint URL directly or use the service-based endpoint update.`,
	Run: func(cmd *cobra.Command, args []string) {
		urlFlag, _ := cmd.Flags().GetString("url")
		listFlag, _ := cmd.Flags().GetBool("list")

		// Get current environment configuration
		settingDir := GetSettingDir()
		settingPath := filepath.Join(settingDir, "setting.yaml")
		appV := viper.New()
		if err := loadSetting(appV, settingPath); err != nil {
			pterm.Error.Printf("Failed to load setting: %v\n", err)
			return
		}

		currentEnv := getCurrentEnvironment(appV)
		if currentEnv == "" {
			pterm.Error.Println("No environment is currently selected.")
			return
		}

		endpoint, err := getEndpoint(appV)
		if err != nil {
			pterm.Error.Printf("Failed to get endpoint: %v\n", err)
			return
		}

		if urlFlag != "" {
			// Check if the URL starts with grpc:// or grpc+ssl://
			if strings.HasPrefix(urlFlag, "grpc://") || strings.HasPrefix(urlFlag, "grpc+ssl://") {
				appV.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), urlFlag)
				if err := appV.WriteConfig(); err != nil {
					pterm.Error.Printf("Failed to update setting.yaml: %v\n", err)
					return
				}
				pterm.Success.Printf("Updated endpoint for '%s' to '%s'.\n", currentEnv, urlFlag)
				return
			}

			if strings.HasSuffix(currentEnv, "-app") {
				pterm.Error.Println("Direct URL endpoint update is not available for user environment.")
				pterm.Info.Println("Please use the service flag (-s) instead.")
				return
			}

			// Handle protocol for endpoint
			if !strings.HasPrefix(urlFlag, "http://") && !strings.HasPrefix(urlFlag, "https://") {
				urlFlag = "https://" + urlFlag
			}

			// Update endpoint directly with URL
			appV.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), urlFlag)
			appV.Set(fmt.Sprintf("environments.%s.proxy", currentEnv), true)

			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update setting.yaml: %v\n", err)
				return
			}
			pterm.Success.Printf("Updated endpoint for '%s' to '%s'.\n", currentEnv, urlFlag)
			return
		}

		var identityEndpoint, restIdentityEndpoint string
		var hasIdentityService bool
		if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
			apiEndpoint, err := transport.GetAPIEndpoint(endpoint)
			if err != nil {
				pterm.Error.Printf("Failed to get API endpoint: %v\n", err)
				return
			}

			identityEndpoint, hasIdentityService, err = transport.GetIdentityEndpoint(apiEndpoint)
			if err != nil {
				pterm.Error.Printf("Failed to get identity endpoint: %v\n", err)
				return
			}
			restIdentityEndpoint = apiEndpoint + "/identity"
		}

		// If list flag is provided, only show available services
		if listFlag {
			// Check if environment is local
			if currentEnv == "local" {
				pterm.Error.Println("Service listing is not available in local environment.")
				return
			}

			token, err := getToken(appV)
			if err != nil {
				if strings.HasSuffix(currentEnv, "-user") {
					pterm.DefaultBox.WithTitle("Authentication Required").
						WithTitleTopCenter().
						WithBoxStyle(pterm.NewStyle(pterm.FgLightCyan)).
						WithRightPadding(4).
						WithLeftPadding(4).
						Println("Please login to SpaceONE Console first.\n" +
							"Run the following command to authenticate:\n\n" +
							"$ cfctl login")
					return
				}
				pterm.Error.Println("Error retrieving token:", err)
				return
			}

			isProxy := appV.GetBool(fmt.Sprintf("environments.%s.proxy", currentEnv))

			if strings.HasPrefix(endpoint, "grpc://") || strings.HasPrefix(endpoint, "grpc+ssl://") {
				if !isProxy {
					pterm.Error.Println("Service listing is only available when proxy is enabled.")
					pterm.DefaultBox.WithTitle("Available Options").
						WithTitleTopCenter().
						WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).
						WithRightPadding(1).
						WithLeftPadding(1).
						Println("Update endpoint to use identity service:\n" +
							"   $ cfctl setting endpoint -s identity\n" +
							"                   Or\n" +
							"Update endpoint with a valid console URL:\n" +
							"   $ cfctl setting endpoint -u example.com")
					return
				}

				var endpoints map[string]string
				parts := strings.Split(endpoint, "/")
				endpoint = strings.Join(parts[:len(parts)-1], "/")
				parts = strings.Split(endpoint, "://")
				if len(parts) != 2 {
					fmt.Errorf("invalid endpoint format: %s", endpoint)
				}

				scheme := parts[0]
				hostPort := parts[1]

				// Configure gRPC connection based on scheme
				var opts []grpc.DialOption
				if scheme == "grpc+ssl" {
					tlsConfig := &tls.Config{
						InsecureSkipVerify: false, // Enable server certificate verification
					}
					creds := credentials.NewTLS(tlsConfig)
					opts = append(opts, grpc.WithTransportCredentials(creds))
				} else {
					opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
				}

				// Establish the connection
				conn, err := grpc.Dial(hostPort, opts...)
				if err != nil {
					fmt.Errorf("connection failed: unable to connect to %s: %v", endpoint, err)
				}
				defer conn.Close()

				// Use Reflection to discover services
				refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
				defer refClient.Reset()

				// Resolve the service and method
				serviceName := "spaceone.api.identity.v2.Endpoint"
				methodName := "list"

				serviceDesc, err := refClient.ResolveService(serviceName)
				if err != nil {
					fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
				}

				methodDesc := serviceDesc.FindMethodByName(methodName)
				if methodDesc == nil {
					fmt.Errorf("method not found: %s", methodName)
				}

				// Dynamically create the request message
				reqMsg := dynamic.NewMessage(methodDesc.GetInputType())

				// Set "query" field (optional)
				queryField := methodDesc.GetInputType().FindFieldByName("query")
				if queryField != nil && queryField.GetMessageType() != nil {
					queryMsg := dynamic.NewMessage(queryField.GetMessageType())
					// Set additional query fields here if needed
					reqMsg.SetFieldByName("query", queryMsg)
				}

				// Prepare an empty response message
				respMsg := dynamic.NewMessage(methodDesc.GetOutputType())

				// Full method name
				fullMethod := fmt.Sprintf("/%s/%s", serviceName, methodName)

				// Invoke the gRPC method
				err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
				if err != nil {
					fmt.Errorf("failed to invoke method %s: %v", fullMethod, err)
				}

				// Process the response to extract `service` and `endpoint`
				endpoints = make(map[string]string)
				resultsField := respMsg.FindFieldDescriptorByName("results")
				if resultsField == nil {
					fmt.Errorf("'results' field not found in response")
				}

				results := respMsg.GetField(resultsField).([]interface{})
				var formattedServices []string
				for _, result := range results {
					resultMsg := result.(*dynamic.Message)
					serviceName := resultMsg.GetFieldByName("service").(string)
					serviceEndpoint := resultMsg.GetFieldByName("endpoint").(string)
					endpoints[serviceName] = serviceEndpoint

					if serviceName == "identity" {
						formattedServices = append(formattedServices, pterm.FgCyan.Sprintf("%s (proxy)", serviceName))
					} else {
						formattedServices = append(formattedServices, pterm.FgDefault.Sprint(serviceName))
					}
				}

				tableData := pterm.TableData{
					{"Service", "Endpoint"},
				}

				services := make([]string, 0, len(endpoints))
				for service := range endpoints {
					services = append(services, service)
				}
				sort.Strings(services)

				for _, service := range services {
					endpoint := endpoints[service]
					if service == "identity" {
						tableData = append(tableData, []string{
							pterm.FgLightCyan.Sprintf("%s (proxy)", service),
							endpoint,
						})
					} else {
						tableData = append(tableData, []string{
							service,
							endpoint,
						})
					}
				}

				pterm.Println("Available Services:")
				pterm.Println()

				pterm.DefaultTable.
					WithHasHeader().
					WithData(tableData).
					WithBoxed(true).
					Render()
			} else if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
				var formattedServices []string
				endpoints, err := fetchAvailableServices(identityEndpoint, restIdentityEndpoint, hasIdentityService, token)
				if err != nil {
					pterm.Error.Println("Error fetching available services:", err)
					return
				}

				if len(endpoints) == 0 {
					pterm.Println("No available services found.")
					return
				}

				for service, endpoint := range endpoints {
					if service == "identity" {
						formattedServices = append(formattedServices, fmt.Sprintf("%s (proxy)\n%s",
							pterm.FgCyan.Sprint(service),
							pterm.FgGray.Sprint(endpoint)))
					} else {
						formattedServices = append(formattedServices, fmt.Sprintf("%s\n%s",
							pterm.FgDefault.Sprint(service),
							pterm.FgGray.Sprint(endpoint)))
					}
				}

				tableData := pterm.TableData{
					{"Service", "Endpoint"},
				}

				services := make([]string, 0, len(endpoints))
				for service := range endpoints {
					services = append(services, service)
				}
				sort.Strings(services)

				for _, service := range services {
					endpoint := endpoints[service]
					if service == "identity" {
						tableData = append(tableData, []string{
							pterm.FgLightCyan.Sprintf("%s (proxy)", service),
							endpoint,
						})
					} else {
						tableData = append(tableData, []string{
							service,
							endpoint,
						})
					}
				}

				pterm.Info.Println("Available Services")

				pterm.DefaultTable.
					WithHasHeader().
					WithData(tableData).
					WithBoxed(true).
					Render()

				return
			}
		}

		// Handle URL flag
		if urlFlag != "" {
			appV.Set(fmt.Sprintf("environments.%s.endpoint", currentEnv), urlFlag)
			if err := appV.WriteConfig(); err != nil {
				pterm.Error.Printf("Failed to update setting.yaml: %v\n", err)
				return
			}
			pterm.Success.Printf("Updated endpoint for '%s' to '%s'.\n", currentEnv, urlFlag)
			return
		}

		// Show help if no flags provided
		pterm.DefaultBox.
			WithTitle("Required Flags").
			WithTitleTopCenter().
			WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).
			WithRightPadding(1).
			WithLeftPadding(1).
			Println("Please use one of the following flags:")

		pterm.Info.Println("To update endpoint URL directly:")
		pterm.Printf("  $ cfctl setting endpoint -u %s\n\n", pterm.FgLightCyan.Sprint("https://example.com"))

		pterm.Info.Println("To list available services:")
		pterm.Printf("  $ cfctl setting endpoint --list\n\n")

		cmd.Help()
	},
}

func invokeGRPCEndpointList(hostPort string, opts []grpc.DialOption) (map[string]string, error) {
	// Wrap the entire operation in a function that can recover from panic
	var endpoints = make(map[string]string)
	var err error

	defer func() {
		if r := recover(); r != nil {
			switch x := r.(type) {
			case string:
				err = fmt.Errorf("error: %s", x)
			case error:
				err = x
			default:
				err = fmt.Errorf("unknown panic: %v", r)
			}
		}
	}()

	// Establish the connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", hostPort, err)
	}
	defer conn.Close()

	// Use Reflection to discover services
	refClient := grpcreflect.NewClient(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	serviceName := "spaceone.api.identity.v2.Endpoint"
	methodName := "list"

	serviceDesc, err := refClient.ResolveService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve service %s: %v", serviceName, err)
	}

	methodDesc := serviceDesc.FindMethodByName(methodName)
	if methodDesc == nil {
		return nil, fmt.Errorf("method not found: %s", methodName)
	}

	reqMsg := dynamic.NewMessage(methodDesc.GetInputType())
	respMsg := dynamic.NewMessage(methodDesc.GetOutputType())
	fullMethod := fmt.Sprintf("/%s/%s", serviceName, methodName)

	err = conn.Invoke(context.Background(), fullMethod, reqMsg, respMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method %s: %v", fullMethod, err)
	}

	resultsField := respMsg.FindFieldDescriptorByName("results")
	if resultsField == nil {
		return nil, fmt.Errorf("'results' field not found in response")
	}

	results := respMsg.GetField(resultsField).([]interface{})
	for _, result := range results {
		resultMsg := result.(*dynamic.Message)
		serviceName := resultMsg.GetFieldByName("service").(string)
		serviceEndpoint := resultMsg.GetFieldByName("endpoint").(string)
		endpoints[serviceName] = serviceEndpoint
	}

	return endpoints, nil
}

// settingTokenCmd updates the token for the current environment
// settingTokenCmd updates the token for the current environment
var settingTokenCmd = &cobra.Command{
	Use:   "token [token_value]",
	Short: "Set the token for the current environment",
	Long:  `Update the token for the current environment.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Load current environment configuration file
		settingDir := GetSettingDir()
		settingPath := filepath.Join(settingDir, "setting.yaml")

		v := viper.New()
		v.SetConfigFile(settingPath)
		v.SetConfigType("yaml")

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

		// Update token
		tokenKey := fmt.Sprintf("environments.%s.token", currentEnv)
		v.Set(tokenKey, args[0])

		// Save configuration
		if err := v.WriteConfig(); err != nil {
			pterm.Error.Printf("Failed to update token: %v\n", err)
			return
		}

		pterm.Success.Printf("Token updated for '%s' environment.\n", currentEnv)
		pterm.Info.Printf("Configuration saved to: %s\n", settingPath)
	},
}

// fetchAvailableServices retrieves the list of services by calling the List method on the Endpoint service.
func fetchAvailableServices(identityEndpoint, restIdentityEndpoint string, hasIdentityEndpoint bool, token string) (map[string]string, error) {
	endpoints := make(map[string]string)

	if !hasIdentityEndpoint {
		// Create HTTP client and request
		client := &http.Client{}

		// Define response structure
		type EndpointResponse struct {
			Results []struct {
				Name     string `json:"name"`
				Service  string `json:"service"`
				Endpoint string `json:"endpoint"`
			} `json:"results"`
			TotalCount int `json:"total_count"`
		}

		// Create and send request
		req, err := http.NewRequest("POST", restIdentityEndpoint+"/endpoint/list", bytes.NewBuffer([]byte("{}")))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %v", err)
		}

		req.Header.Set("accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to send request: %v", err)
		}
		defer resp.Body.Close()

		// Parse response
		var response EndpointResponse
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, fmt.Errorf("failed to decode response: %v", err)
		}

		// Extract services
		for _, result := range response.Results {
			endpoints[result.Service] = result.Endpoint
		}

		return endpoints, nil
	} else {
		parsedURL, err := url.Parse(identityEndpoint)
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
		if strings.HasPrefix(identityEndpoint, "grpc+ssl://") {
			tlsSetting := &tls.Config{
				InsecureSkipVerify: false, // Set to true only if you want to skip TLS verification (not recommended)
			}
			creds := credentials.NewTLS(tlsSetting)
			opts = append(opts, grpc.WithTransportCredentials(creds))
		} else {
			return nil, fmt.Errorf("unsupported scheme in endpoint: %s", identityEndpoint)
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

		for _, res := range resultsSlice {
			resMsg, ok := res.(*dynamic.Message)
			if !ok {
				continue
			}

			// Extract service field
			serviceFieldDesc := resMsg.GetMessageDescriptor().FindFieldByName("service")
			if serviceFieldDesc == nil {
				continue
			}

			serviceField, err := resMsg.TryGetField(serviceFieldDesc)
			if err != nil {
				continue
			}

			serviceStr, ok := serviceField.(string)
			if !ok {
				continue
			}

			// Extract endpoint field
			endpointFieldDesc := resMsg.GetMessageDescriptor().FindFieldByName("endpoint")
			if endpointFieldDesc == nil {
				continue
			}

			endpointField, err := resMsg.TryGetField(endpointFieldDesc)
			if err != nil {
				continue
			}

			endpointStr, ok := endpointField.(string)
			if !ok {
				continue
			}

			endpoints[serviceStr] = endpointStr
		}

		return endpoints, nil
	}
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
func getEndpoint(v *viper.Viper) (string, error) {
	currentEnv := getCurrentEnvironment(v)
	if currentEnv == "" {
		return "", fmt.Errorf("no environment is set")
	}

	baseURL := v.GetString(fmt.Sprintf("environments.%s.endpoint", currentEnv))

	if baseURL == "" {
		return "", fmt.Errorf("no endpoint found for environment '%s' in setting.yaml", currentEnv)

	}

	return baseURL, nil
}

// getToken retrieves the token for the current environment.
func getToken(v *viper.Viper) (string, error) {
	currentEnv := getCurrentEnvironment(v)
	if currentEnv == "" {
		return "", fmt.Errorf("no environment selected")
	}

	if strings.HasSuffix(currentEnv, "-app") {
		token := v.GetString(fmt.Sprintf("environments.%s.token", currentEnv))
		if token == "" {
			return "", fmt.Errorf("token not found in settings for environment: %s", currentEnv)
		}
		return token, nil
	}

	if strings.HasSuffix(currentEnv, "-user") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %v", err)
		}

		tokenPath := filepath.Join(home, ".cfctl", "cache", currentEnv, "access_token")
		tokenBytes, err := os.ReadFile(tokenPath)
		if err != nil {
			return "", fmt.Errorf("failed to read token: %v", err)
		}

		return strings.TrimSpace(string(tokenBytes)), nil
	}

	return "", fmt.Errorf("unsupported environment type: %s", currentEnv)
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
	v.SetConfigType("yaml")

	// Read the setting file
	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			// Initialize with default values if file doesn't exist
			defaultSettings := map[string]interface{}{
				"environments": map[string]interface{}{},
				"environment":  "",
			}

			// Write the default settings to file
			if err := v.MergeConfigMap(defaultSettings); err != nil {
				return fmt.Errorf("failed to merge default settings: %w", err)
			}

			if err := v.WriteConfig(); err != nil {
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
	settingPath := filepath.Join(GetSettingDir(), "setting.yaml")
	v := viper.New()

	v.SetConfigFile(settingPath)

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			pterm.Success.WithShowLineNumber(false).Printfln("Global setting updated with existing environments. (default: %s/setting.yaml)", GetSettingDir())
			return
		}
		pterm.Warning.Printf("Warning: Could not read global setting: %v\n", err)
		return
	}

	pterm.Success.WithShowLineNumber(false).Printfln("Global setting updated with existing environments. (default: %s/setting.yaml)", GetSettingDir())
}

func parseEnvNameFromURL(urlStr string) (string, error) {
	isGRPC := strings.HasPrefix(urlStr, "grpc://") || strings.HasPrefix(urlStr, "grpc+ssl://")

	urlStr = strings.TrimPrefix(urlStr, "https://")
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "grpc://")
	urlStr = strings.TrimPrefix(urlStr, "grpc+ssl://")

	if isGRPC {
		return "local", nil
	}

	if strings.Contains(urlStr, "localhost") {
		return "local", nil
	}

	hostParts := strings.Split(urlStr, ":")
	hostname := hostParts[0]

	parts := strings.Split(hostname, ".")

	if isIPAddress(hostname) {
		return "local", nil
	}

	if len(parts) > 0 {
		envName := parts[0]
		reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)
		envName = reg.ReplaceAllString(envName, "")
		return strings.ToLower(envName), nil
	}

	return "", fmt.Errorf("could not determine environment name from URL: %s", urlStr)
}

func isIPAddress(host string) bool {
	ipv4Pattern := `^(\d{1,3}\.){3}\d{1,3}$`
	match, _ := regexp.MatchString(ipv4Pattern, host)
	return match
}

// updateSetting updates the configuration files
func updateSetting(envName, endpoint, envSuffix string) {
	settingDir := GetSettingDir()
	mainSettingPath := filepath.Join(settingDir, "setting.yaml")

	v := viper.New()
	v.SetConfigFile(mainSettingPath)
	v.SetConfigType("yaml")

	// Read existing config if it exists
	_ = v.ReadInConfig()

	// Set environment
	v.Set("environment", envName)

	// Handle protocol for endpoint
	if !strings.Contains(endpoint, "://") {
		if strings.Contains(endpoint, "localhost") || strings.Contains(endpoint, "127.0.0.1") {
			endpoint = "http://" + endpoint
		} else {
			endpoint = "https://" + endpoint
		}
	}

	// Set endpoint in environments map
	envKey := fmt.Sprintf("environments.%s.endpoint", envName)
	v.Set(envKey, endpoint)

	proxyKey := fmt.Sprintf("environments.%s.proxy", envName)
	if strings.HasPrefix(endpoint, "grpc://") || strings.HasPrefix(endpoint, "grpc+ssl://") {
		isProxy, err := transport.CheckIdentityProxyAvailable(endpoint)
		if err != nil {
			pterm.Warning.Printf("Failed to check gRPC endpoint: %v\n", err)
			v.Set(proxyKey, true)
		} else {
			v.Set(proxyKey, isProxy)
		}
	} else {
		v.Set(proxyKey, true)
	}

	// Set token for non-user environments
	if envSuffix != "user" {
		tokenKey := fmt.Sprintf("environments.%s.token", envName)
		v.Set(tokenKey, "no_token")
	}

	if err := v.WriteConfig(); err != nil {
		pterm.Error.Printf("Failed to write setting file: %v\n", err)
		return
	}

	pterm.Success.Printf("Environment '%s' successfully initialized.\n", envName)
	pterm.Info.Printf("Configuration saved to: %s\n", mainSettingPath)
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

func init() {
	SettingCmd.AddCommand(settingInitCmd)
	SettingCmd.AddCommand(settingEndpointCmd)
	SettingCmd.AddCommand(settingTokenCmd)
	SettingCmd.AddCommand(envCmd)
	SettingCmd.AddCommand(showCmd)
	settingInitCmd.AddCommand(settingInitProxyCmd)
	settingInitCmd.AddCommand(settingInitStaticCmd)

	settingInitProxyCmd.Flags().Bool("app", false, "Initialize as application configuration")
	settingInitProxyCmd.Flags().Bool("user", false, "Initialize as user-specific configuration")

	envCmd.Flags().StringP("switch", "s", "", "Switch to a different environment")
	envCmd.Flags().StringP("remove", "r", "", "Remove an environment")
	envCmd.Flags().BoolP("list", "l", false, "List available environments")

	showCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml/json)")

	settingEndpointCmd.Flags().StringP("url", "u", "", "Direct URL to set as endpoint")
	settingEndpointCmd.Flags().BoolP("list", "l", false, "List available services")
}
