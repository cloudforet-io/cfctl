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
)

var cfgFile string

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
	if len(os.Args) >= 2 && (os.Args[1] == "config" ||
		os.Args[1] == "login" ||
		os.Args[1] == "api-resources") { // Add api-resources to skip list
		return
	}

	pterm.Warning.Printf("No valid configuration found.\n")
	pterm.Info.Println("Please run 'cfctl config init' to set up your configuration.")
	pterm.Info.Println("After initialization, run 'cfctl login' to authenticate.")
}

func addDynamicServiceCommands() error {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Convert endpoint to identity endpoint if necessary
	endpoint := config.Endpoint
	if !strings.Contains(endpoint, "identity") {
		parts := strings.Split(endpoint, "://")
		if len(parts) == 2 {
			hostParts := strings.Split(parts[1], ".")
			if len(hostParts) >= 4 {
				env := hostParts[2] // dev or stg
				endpoint = fmt.Sprintf("grpc+ssl://identity.api.%s.spaceone.dev:443", env)
			}
		}
	}

	// Fetch available microservices
	endpointsMap, err := other.FetchEndpointsMap(endpoint)
	if err != nil {
		return fmt.Errorf("failed to fetch services: %v", err)
	}

	// Create and register commands for each service
	for serviceName := range endpointsMap {
		cmd := createServiceCommand(serviceName)
		rootCmd.AddCommand(cmd)
	}

	return nil
}

// loadConfig loads configuration from both main and cache config files
func loadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}

	configFile := filepath.Join(home, ".cfctl", "config.yaml")
	cacheConfigFile := filepath.Join(home, ".cfctl", "cache", "config.yaml")

	// Try to read main config first
	mainV := viper.New()
	mainV.SetConfigFile(configFile)
	mainConfigErr := mainV.ReadInConfig()

	if mainConfigErr != nil {
		return nil, fmt.Errorf("failed to read config file")
	}

	var currentEnv string
	var endpoint string
	var token string

	// Main config exists, try to get environment
	currentEnv = mainV.GetString("environment")
	if currentEnv != "" {
		envConfig := mainV.Sub(fmt.Sprintf("environments.%s", currentEnv))
		if envConfig != nil {
			endpoint = envConfig.GetString("endpoint")
			token = envConfig.GetString("token")
		}
	}

	// If main config doesn't have what we need, try cache config
	if endpoint == "" || token == "" {
		cacheV := viper.New()

		cacheV.SetConfigFile(cacheConfigFile)
		if err := cacheV.ReadInConfig(); err == nil {
			// If no current environment set, try to get it from cache config
			if currentEnv == "" {
				currentEnv = cacheV.GetString("environment")
			}

			// Try to get environment config from cache
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
