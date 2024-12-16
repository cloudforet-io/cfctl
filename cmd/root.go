package cmd

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/spf13/viper"

	"github.com/cloudforet-io/cfctl/cmd/common"
	"github.com/cloudforet-io/cfctl/cmd/other"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
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
	args := os.Args[1:]

	if len(args) > 1 {
		// Check if the first argument is a service name and second is a short name
		v := viper.New()
		if home, err := os.UserHomeDir(); err == nil {
			settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
			v.SetConfigFile(settingPath)
			v.SetConfigType("yaml")

			if err := v.ReadInConfig(); err == nil {
				serviceName := args[0]
				shortName := args[1]
				if command := v.GetString(fmt.Sprintf("short_names.%s.%s", serviceName, shortName)); command != "" {
					// Replace the short name with the actual command
					newArgs := append([]string{args[0]}, strings.Fields(command)...)
					newArgs = append(newArgs, args[2:]...)
					os.Args = append([]string{os.Args[0]}, newArgs...)
				}
			}
		}
	}

	if err := rootCmd.Execute(); err != nil {
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

	done := make(chan bool)
	go func() {
		if endpoints, err := loadCachedEndpoints(); err == nil {
			cachedEndpointsMap = endpoints
		}
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		fmt.Fprintf(os.Stderr, "Warning: Cache loading timed out\n")
	}

	if len(os.Args) > 1 && os.Args[1] == "__complete" {
		pterm.DisableColor()
	}

	// Determine if the current command is 'setting environment -l'
	skipDynamicCommands := false
	if len(os.Args) >= 3 && os.Args[1] == "setting" && os.Args[2] == "environment" {
		for _, arg := range os.Args[3:] {
			if arg == "-l" || arg == "--list" {
				skipDynamicCommands = true
				break
			}
		}
	}

	if !skipDynamicCommands {
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
	rootCmd.AddCommand(other.ShortNameCmd)

	// Set default group for commands without a group
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() != "help" && cmd.Name() != "completion" && cmd.GroupID == "" {
			cmd.GroupID = "other"
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Unable to find home directory: %v", err)
	}
	viper.AddConfigPath(filepath.Join(home, ".cfctl"))
	viper.SetConfigName("setting")
	viper.SetConfigType("yaml")
}

// showInitializationGuide displays a helpful message when configuration is missing
func showInitializationGuide(originalErr error) {
	// Skip showing guide for certain commands
	if len(os.Args) >= 2 && (os.Args[1] == "setting" ||
		os.Args[1] == "login" ||
		os.Args[1] == "api-resources") {
		return
	}

	// Get current environment from setting file
	home, err := os.UserHomeDir()
	if err != nil {
		pterm.Error.Printf("Unable to find home directory: %v\n", err)
		return
	}

	settingFile := filepath.Join(home, ".cfctl", "setting.yaml")
	mainV := viper.New()
	mainV.SetConfigFile(settingFile)
	mainV.SetConfigType("yaml")

	if err := mainV.ReadInConfig(); err != nil {
		pterm.Warning.Printf("No valid configuration found.\n")
		pterm.Info.Println("Please run 'cfctl setting init' to set up your configuration.")
		return
	}

	currentEnv := mainV.GetString("environment")
	if currentEnv == "" {
		pterm.Warning.Printf("No environment selected.\n")
		pterm.Info.Println("Please run 'cfctl setting init' to set up your configuration.")
		return
	}

	// Check if current environment is app type and token is empty
	if strings.HasSuffix(currentEnv, "-app") {
		envConfig := mainV.Sub(fmt.Sprintf("environments.%s", currentEnv))
		if envConfig == nil || envConfig.GetString("token") == "" {
			// Get URL from environment config
			url := envConfig.GetString("url")
			if url == "" {
				// Fallback URL if not specified in config
				parts := strings.Split(currentEnv, "-")
				if len(parts) >= 2 {
					serviceName := parts[0]   // cloudone, spaceone, etc.
					url = fmt.Sprintf("https://%s.console.dev.spaceone.dev", serviceName)
				} else {
					url = "https://console.spaceone.dev"
				}
			}

			pterm.DefaultBox.
				WithTitle("Token Not Found").
				WithTitleTopCenter().
				WithBoxStyle(pterm.NewStyle(pterm.FgWhite)).
				WithRightPadding(1).
				WithLeftPadding(1).
				WithTopPadding(0).
				WithBottomPadding(0).
				Println("Please follow the instructions below to obtain an App Token.")

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
				pterm.FgLightYellow.Sprint(settingFile),
				pterm.FgLightGreen.Sprint(currentEnv))

			pterm.DefaultBox.
				WithTitle("Setup Instructions").
				WithTitleTopCenter().
				WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).
				Println(boxContent)

			pterm.Info.Println("After updating the token, please try your command again.")
		}
	} else if strings.HasSuffix(currentEnv, "-user") {
		pterm.Warning.Printf("Authentication required.\n")
		pterm.Info.Println("To see Available Commands, please authenticate first:")
		pterm.Info.Println("$ cfctl login")
	}
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

	// Load configuration
	setting, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load setting: %v", err)
	}

	// Handle local environment
	if strings.HasPrefix(setting.Environment, "local-") {
		// Try connecting to local gRPC server
		conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure(), grpc.WithBlock(), grpc.WithTimeout(2*time.Second))
		if err != nil {
			pterm.Error.Printf("Cannot connect to local gRPC server (grpc://localhost:50051)\n")
			pterm.Info.Println("Please check if your gRPC server is running")
			return fmt.Errorf("local gRPC server connection failed: %v", err)
		}
		defer conn.Close()

		// Create reflection client
		ctx := context.Background()
		refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
		defer refClient.Reset()

		// List all services
		services, err := refClient.ListServices()
		if err != nil {
			return fmt.Errorf("failed to list local services: %v", err)
		}

		endpointsMap := make(map[string]string)
		for _, svc := range services {
			if strings.HasPrefix(svc, "spaceone.api.") {
				parts := strings.Split(svc, ".")
				if len(parts) >= 4 {
					serviceName := parts[2]
					// Skip core service
					if serviceName != "core" {
						endpointsMap[serviceName] = "grpc://localhost:50051"
					}
				}
			}
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

	// Continue with existing logic for non-local environments
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

	cachedEndpointsMap = endpointsMap
	if err := saveEndpointsCache(endpointsMap); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to cache endpoints: %v\n", err)
	}

	for serviceName := range endpointsMap {
		cmd := createServiceCommand(serviceName)
		rootCmd.AddCommand(cmd)
	}

	return nil
}


func loadCachedEndpoints() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	settingFile := filepath.Join(home, ".cfctl", "setting.yaml")
	settingData, err := os.ReadFile(settingFile)
	if err != nil {
		return nil, err
	}

	var settings struct {
		Environment string `yaml:"environment"`
	}

	if err := yaml.Unmarshal(settingData, &settings); err != nil {
		return nil, err
	}

	if settings.Environment == "" {
		return nil, fmt.Errorf("no environment set")
	}

	cacheFile := filepath.Join(home, ".cfctl", "cache", settings.Environment, "endpoints.yaml")
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}

	cacheInfo, err := os.Stat(cacheFile)
	if err != nil {
		return nil, err
	}

	if time.Since(cacheInfo.ModTime()) > 24*time.Hour {
		return nil, fmt.Errorf("cache expired")
	}

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

	// Get current environment from main setting file
	mainV := viper.New()
	mainV.SetConfigFile(filepath.Join(home, ".cfctl", "setting.yaml"))
	mainV.SetConfigType("yaml")
	if err := mainV.ReadInConfig(); err != nil {
		return err
	}

	currentEnv := mainV.GetString("environment")
	if currentEnv == "" {
		return fmt.Errorf("no environment set")
	}

	// Create environment-specific cache directory
	envCacheDir := filepath.Join(home, ".cfctl", "cache", currentEnv)
	if err := os.MkdirAll(envCacheDir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(endpoints)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(envCacheDir, "endpoints.yaml"), data, 0644)
}

// loadConfig loads configuration from both main and cache setting files
func loadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}

	settingFile := filepath.Join(home, ".cfctl", "setting.yaml")

	// Read main setting file
	mainV := viper.New()
	mainV.SetConfigFile(settingFile)
	mainV.SetConfigType("yaml")
	if err := mainV.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read setting file")
	}

	currentEnv := mainV.GetString("environment")
	if currentEnv == "" {
		return nil, fmt.Errorf("no environment set")
	}

	// Get environment config
	envConfig := mainV.Sub(fmt.Sprintf("environments.%s", currentEnv))
	if envConfig == nil {
		return nil, fmt.Errorf("environment %s not found", currentEnv)
	}

	endpoint := envConfig.GetString("endpoint")
	if endpoint == "" {
		return nil, fmt.Errorf("no endpoint found in configuration")
	}

	var token string
	// Check environment suffix
	if strings.HasSuffix(currentEnv, "-user") {
		// For user environments, read from cache directory
		envCacheDir := filepath.Join(home, ".cfctl", "cache", currentEnv)
		grantTokenPath := filepath.Join(envCacheDir, "grant_token")
		data, err := os.ReadFile(grantTokenPath)
		if err != nil {
			return nil, fmt.Errorf("no valid token found in cache")
		}
		token = string(data)
	} else if strings.HasSuffix(currentEnv, "-app") {
		token = envConfig.GetString("token")
		if token == "" {
			return nil, fmt.Errorf("no token found in configuration")
		}
	} else {
		return nil, fmt.Errorf("invalid environment suffix: must end with -user or -app")
	}

	return &Config{
		Environment: currentEnv,
		Endpoint:    endpoint,
		Token:       token,
	}, nil
}

func createServiceCommand(serviceName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   serviceName,
		Short: fmt.Sprintf("Interact with the %s service", serviceName),
		Long: fmt.Sprintf(`Use this command to interact with the %s service.

%s

%s`,
			serviceName,
			pterm.DefaultBox.WithTitle("Interactive Mode").WithTitleTopCenter().Sprint(
				func() string {
					str, _ := pterm.DefaultBulletList.WithItems([]pterm.BulletListItem{
						{Level: 0, Text: "Required parameters will be prompted if not provided"},
						{Level: 0, Text: "Missing parameters will be requested interactively"},
						{Level: 0, Text: "Just follow the prompts to fill in the required fields"},
					}).Srender()
					return str
				}()),
			pterm.DefaultBox.WithTitle("Example").WithTitleTopCenter().Sprint(
				fmt.Sprintf("Instead of:\n"+
					"  $ cfctl %s <Verb> <Resource> -p key=value\n\n"+
					"You can simply run:\n"+
					"  $ cfctl %s <Verb> <Resource>\n\n"+
					"The tool will interactively prompt for the required parameters.",
					serviceName, serviceName))),
		GroupID: "available",
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no args provided, show available verbs
			if len(args) == 0 {
				common.PrintAvailableVerbs(cmd)
				return nil
			}

			// Process command arguments
			if len(args) < 2 {
				return cmd.Help()
			}

			verb := args[0]
			resource := args[1]

			// Create options from remaining args
			options := &common.FetchOptions{
				Parameters: make([]string, 0),
			}

			// Process remaining args as parameters
			for i := 2; i < len(args); i++ {
				if strings.HasPrefix(args[i], "--") {
					paramName := strings.TrimPrefix(args[i], "--")
					if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
						options.Parameters = append(options.Parameters, fmt.Sprintf("%s=%s", paramName, args[i+1]))
						i++
					}
				}
			}

			// Call FetchService with the processed arguments
			result, err := common.FetchService(serviceName, verb, resource, options)
			if err != nil {
				pterm.Error.Printf("Failed to execute command: %v\n", err)
				return err
			}

			if result != nil {
				// The result will be printed by FetchService if needed
				return nil
			}

			return nil
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
