package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/cloudforet-io/cfctl/cmd/common"
	"github.com/cloudforet-io/cfctl/cmd/other"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var cfgFile string
var cachedEndpointsMap map[string]string

// Config represents the configuration structure
type Config struct {
	Environment string
	Endpoint    string
	Token       string
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cfctl",
	Short: "cfctl controls the SpaceONE services.",
	Long: `cfctl controls the SpaceONE services.
  Find more information at: 
    - https://github.com/cloudforet-io/cfctl
    - https://docs.spaceone.megazone.io/developers/setup/cfctl (English)
    - https://docs.spaceone.megazone.io/ko/developers/setup/cfctl (Korean)`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Initialize available commands group
	AvailableCommands := &cobra.Group{
		ID:    "available",
		Title: "Available Commands:",
	}
	rootCmd.AddGroup(AvailableCommands)

	if len(os.Args) > 1 && os.Args[1] == "__complete" {
		pterm.DisableColor()
	}

	// Skip configuration check for settings init commands
	if len(os.Args) >= 3 && os.Args[1] == "settings" && os.Args[2] == "init" {
		// Skip configuration check for initialization
	} else {
		// Try to add dynamic service commands
		if err := addDynamicServiceCommands(); err != nil {
			showInitializationGuide(err)
		}
	}

	// Initialize other commands group
	OtherCommands := &cobra.Group{
		ID:    "other",
		Title: "Other Commands:",
	}
	rootCmd.AddGroup(OtherCommands)
	rootCmd.AddCommand(other.ApiResourcesCmd)
	rootCmd.AddCommand(other.SettingCmd)
	rootCmd.AddCommand(other.LoginCmd)

	// Set default group for commands without a group
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() != "help" && cmd.Name() != "completion" && cmd.GroupID == "" {
			cmd.GroupID = "other"
		}
	}
}

// showInitializationGuide displays a helpful message when configuration is missing
func showInitializationGuide(originalErr error) {
	// Only show error message for commands that require configuration
	if len(os.Args) >= 2 && (os.Args[1] == "setting" ||
		os.Args[1] == "login" ||
		os.Args[1] == "api-resources") { // Add api-resources to skip list
		return
	}

	pterm.Warning.Printf("No valid configuration found.\n")
	pterm.Info.Println("Please run 'cfctl setting init' to set up your configuration.")
	pterm.Info.Println("After initialization, run 'cfctl login' to authenticate.")
}

func addDynamicServiceCommands() error {
	// If we already have in-memory cache, use it
	if cachedEndpointsMap != nil {
		for serviceName := range cachedEndpointsMap {
			cmd := createServiceCommand(serviceName)
			rootCmd.AddCommand(cmd)
		}
		return nil
	}

	// Try to load endpoints from file cache
	endpoints, err := loadCachedEndpoints()
	if err == nil {
		// Store in memory for subsequent calls
		cachedEndpointsMap = endpoints

		// Create commands using cached endpoints
		for serviceName := range endpoints {
			cmd := createServiceCommand(serviceName)
			rootCmd.AddCommand(cmd)
		}
		return nil
	}

	// If no cache available, fetch dynamically (this is slow path)
	setting, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load setting: %v", err)
	}

	endpoint := setting.Endpoint
	if !strings.Contains(endpoint, "identity") {
		parts := strings.Split(endpoint, "://")
		if len(parts) == 2 {
			hostParts := strings.Split(parts[1], ".")
			if len(hostParts) >= 4 {
				env := hostParts[2]
				endpoint = fmt.Sprintf("grpc+ssl://identity.api.%s.spaceone.dev:443", env)
			}
		}
	}

	endpointsMap, err := other.FetchEndpointsMap(endpoint)
	if err != nil {
		return fmt.Errorf("failed to fetch services: %v", err)
	}

	// Store in both memory and file cache
	cachedEndpointsMap = endpointsMap
	if err := saveEndpointsCache(endpointsMap); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to cache endpoints: %v\n", err)
	}

	// Create commands for each service
	for serviceName := range endpointsMap {
		cmd := createServiceCommand(serviceName)
		rootCmd.AddCommand(cmd)
	}

	return nil
}

func clearEndpointsCache() {
	cachedEndpointsMap = nil
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	cacheFile := filepath.Join(home, ".cfctl", "cache", "endpoints.yaml")
	os.Remove(cacheFile)
}

func loadCachedEndpoints() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Read from cache file
	cacheFile := filepath.Join(home, ".cfctl", "cache", "endpoints.yaml")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	// Parse cached endpoints
	var endpoints map[string]string
	if err := yaml.Unmarshal(data, &endpoints); err != nil {
		return nil, err
	}

	return endpoints, nil
}

func saveEndpointsCache(endpoints map[string]string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Ensure cache directory exists
	cacheDir := filepath.Join(home, ".cfctl", "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return err
	}

	// Marshal endpoints to YAML format
	data, err := yaml.Marshal(endpoints)
	if err != nil {
		return err
	}

	// Write to cache file
	return os.WriteFile(filepath.Join(cacheDir, "endpoints.yaml"), data, 0644)
}

// loadConfig loads configuration from both main and cache setting files
func loadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}

	settingFile := filepath.Join(home, ".cfctl", "setting.yaml")
	cacheConfigFile := filepath.Join(home, ".cfctl", "cache", "setting.yaml")

	// Try to read main setting first
	mainV := viper.New()
	mainV.SetConfigFile(settingFile)
	mainConfigErr := mainV.ReadInConfig()

	if mainConfigErr != nil {
		return nil, fmt.Errorf("failed to read setting file")
	}

	var currentEnv string
	var endpoint string
	var token string

	// Main setting exists, try to get environment
	currentEnv = mainV.GetString("environment")
	if currentEnv != "" {
		envConfig := mainV.Sub(fmt.Sprintf("environments.%s", currentEnv))
		if envConfig != nil {
			endpoint = envConfig.GetString("endpoint")
			token = envConfig.GetString("token")
		}
	}

	// If main setting doesn't have what we need, try cache setting
	if endpoint == "" || token == "" {
		cacheV := viper.New()

		cacheV.SetConfigFile(cacheConfigFile)
		if err := cacheV.ReadInConfig(); err == nil {
			// If no current environment set, try to get it from cache setting
			if currentEnv == "" {
				currentEnv = cacheV.GetString("environment")
			}

			// Try to get environment setting from cache
			if currentEnv != "" {
				envConfig := cacheV.Sub(fmt.Sprintf("environments.%s", currentEnv))
				if envConfig != nil {
					if endpoint == "" {
						endpoint = envConfig.GetString("endpoint")
					}
					if token == "" {
						token = envConfig.GetString("token")
					}
				}
			}

			// If still no environment, try to find first user environment
			if currentEnv == "" {
				envs := cacheV.GetStringMap("environments")
				for env := range envs {
					if strings.HasSuffix(env, "-user") {
						currentEnv = env
						envConfig := cacheV.Sub(fmt.Sprintf("environments.%s", currentEnv))
						if envConfig != nil {
							if endpoint == "" {
								endpoint = envConfig.GetString("endpoint")
							}
							if token == "" {
								token = envConfig.GetString("token")
							}
							break
						}
					}
				}
			}
		}
	}

	if endpoint == "" {
		return nil, fmt.Errorf("no endpoint found in configuration")
	}

	if token == "" {
		return nil, fmt.Errorf("no token found in configuration")
	}

	return &Config{
		Environment: currentEnv,
		Endpoint:    endpoint,
		Token:       token,
	}, nil
}

func createServiceCommand(serviceName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     serviceName,
		Short:   fmt.Sprintf("Interact with the %s service", serviceName),
		Long:    fmt.Sprintf(`Use this command to interact with the %s service.`, serviceName),
		GroupID: "available",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 0 {
				common.PrintAvailableVerbs(cmd)
				return
			}
			cmd.Help()
		},
	}

	cmd.AddGroup(&cobra.Group{
		ID:    "available",
		Title: "Available Commands:",
	}, &cobra.Group{
		ID:    "other",
		Title: "Other Commands:",
	})

	cmd.SetHelpFunc(common.CustomParentHelpFunc)

	apiResourcesCmd := common.FetchApiResourcesCmd(serviceName)
	apiResourcesCmd.GroupID = "available"
	cmd.AddCommand(apiResourcesCmd)

	err := common.AddVerbCommands(cmd, serviceName, "other")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding verb commands for %s: %v\n", serviceName, err)
	}

	return cmd
}
