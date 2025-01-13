package other

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudforet-io/cfctl/pkg/transport"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var service string

// ShortNameCmd represents the shortName command
var ShortNameCmd = &cobra.Command{
	Use:   "short_name",
	Short: "Manage short names for commands",
	Long:  `Manage short names for frequently used commands.`,
}

// validateServiceCommand checks if the given verb and resource are valid for the service
func validateServiceCommand(service, verb, resource string) error {
	// Get current environment from main setting file
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	mainV := viper.New()
	mainV.SetConfigFile(filepath.Join(home, ".cfctl", "setting.yaml"))
	mainV.SetConfigType("yaml")
	if err := mainV.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	currentEnv := mainV.GetString("environment")
	if currentEnv == "" {
		return fmt.Errorf("no environment set")
	}

	// Get environment config
	envConfig := mainV.Sub(fmt.Sprintf("environments.%s", currentEnv))
	if envConfig == nil {
		return fmt.Errorf("environment %s not found", currentEnv)
	}

	endpoint := envConfig.GetString("endpoint")
	if endpoint == "" {
		return fmt.Errorf("no endpoint found in configuration")
	}

	// Fetch endpoints map
	endpointsMap, err := transport.FetchEndpointsMap(endpoint)
	if err != nil {
		return fmt.Errorf("failed to fetch endpoints: %v", err)
	}

	// Check if service exists
	serviceEndpoint, ok := endpointsMap[service]
	if !ok {
		return fmt.Errorf("service '%s' not found", service)
	}

	// Fetch service resources
	resources, err := fetchServiceResources(service, serviceEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch service resources: %v", err)
	}

	// Find the resource and check if the verb is valid
	resourceFound := false
	verbFound := false

	for _, row := range resources {
		if row[1] == resource { // row[1] is the resource name
			resourceFound = true
			verbs := strings.Split(row[3], ", ") // row[3] contains the verbs
			for _, v := range verbs {
				if v == verb {
					verbFound = true
					break
				}
			}
			break
		}
	}

	if !resourceFound {
		return fmt.Errorf("resource '%s' not found in service '%s'", resource, service)
	}

	if !verbFound {
		return fmt.Errorf("verb '%s' not found for resource '%s' in service '%s'", verb, resource, service)
	}

	return nil
}

var addShortNameCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new short name",
	Example: `  $ cfctl short_name -s inventory add -n job -c "list Job"

  Then use them as:
  $ cfctl inventory job     # This command is same as $ cfctl inventory list Job`,
	Run: func(cmd *cobra.Command, args []string) {
		// Show example if no flags are provided
		if !cmd.Flags().Changed("name") || !cmd.Flags().Changed("command") || !cmd.Flags().Changed("service") {
			pterm.DefaultBox.
				WithTitle("Short Name Examples").
				WithTitleTopCenter().
				WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).
				Println(`Example:
  $ cfctl short_name -s inventory add -n job -c "list Job"

Then use them as:
  $ cfctl inventory job     # This command is same as $ cfctl inventory list Job`)
			return
		}

		shortName, _ := cmd.Flags().GetString("name")
		command, _ := cmd.Flags().GetString("command")
		service, _ := cmd.Flags().GetString("service")

		// Parse command to get verb and resource
		parts := strings.Fields(command)
		if len(parts) < 2 {
			pterm.Error.Printf("Invalid command format. Expected '<verb> <resource>', got '%s'\n", command)
			return
		}

		verb := parts[0]
		resource := parts[1]

		// Validate the command
		if err := validateServiceCommand(service, verb, resource); err != nil {
			pterm.Error.Printf("Invalid command: %v\n", err)
			return
		}

		if err := addShortName(service, shortName, command); err != nil {
			pterm.Error.Printf("Failed to add short name: %v\n", err)
			return
		}

		pterm.Success.Printf("Successfully added short name '%s' for service '%s' command '%s'\n", shortName, service, command)
	},
}

var removeShortNameCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a short name",
	Run: func(cmd *cobra.Command, args []string) {
		shortName, err := cmd.Flags().GetString("name")
		service, _ := cmd.Flags().GetString("service")
		if err != nil || shortName == "" || service == "" {
			pterm.Error.Println("The --name (-n) and --service (-s) flags are required")
			cmd.Help()
			return
		}

		if err := removeShortName(service, shortName); err != nil {
			pterm.Error.Printf("Failed to remove short name: %v\n", err)
			return
		}

		pterm.Success.Printf("Successfully removed short name '%s' from service '%s'\n", shortName, service)
	},
}

var listShortNameCmd = &cobra.Command{
	Use:   "list",
	Short: "List all short names",
	Run: func(cmd *cobra.Command, args []string) {
		shortNames, err := listShortNames()
		if err != nil {
			pterm.Error.Printf("Failed to list short names: %v\n", err)
			return
		}

		if len(shortNames) == 0 {
			pterm.Info.Println("No short names found")
			return
		}

		// Create table
		table := pterm.TableData{
			{"Service", "Short Name", "Command"},
		}

		// Add short names to table
		for service, serviceShortNames := range shortNames {
			for name, command := range serviceShortNames.(map[string]interface{}) {
				table = append(table, []string{service, name, command.(string)})
			}
		}

		// Print table
		pterm.DefaultTable.WithHasHeader().WithData(table).Render()
	},
}

func addShortName(service, shortName, command string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read config: %v", err)
	}

	v.Set(fmt.Sprintf("short_names.%s.%s", service, shortName), command)

	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func removeShortName(service, shortName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	// Check if service and short name exist
	if !v.IsSet(fmt.Sprintf("short_names.%s.%s", service, shortName)) {
		return fmt.Errorf("short name '%s' not found in service '%s'", shortName, service)
	}

	// Get all short names for the service
	serviceShortNames := v.GetStringMap(fmt.Sprintf("short_names.%s", service))
	delete(serviceShortNames, shortName)

	// Update config with removed short name
	v.Set(fmt.Sprintf("short_names.%s", service), serviceShortNames)

	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func listShortNames() (map[string]interface{}, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	shortNames := v.GetStringMap("short_names")
	if shortNames == nil {
		return make(map[string]interface{}), nil
	}

	return shortNames, nil
}

func init() {
	ShortNameCmd.AddCommand(addShortNameCmd)
	ShortNameCmd.AddCommand(removeShortNameCmd)
	ShortNameCmd.AddCommand(listShortNameCmd)

	ShortNameCmd.PersistentFlags().StringVarP(&service, "service", "s", "", "Service to manage short names for")
	addShortNameCmd.MarkPersistentFlagRequired("service")
	removeShortNameCmd.MarkPersistentFlagRequired("service")

	addShortNameCmd.Flags().StringP("name", "n", "", "Short name to add")
	addShortNameCmd.Flags().StringP("command", "c", "", "Command to execute")
	addShortNameCmd.MarkFlagRequired("name")
	addShortNameCmd.MarkFlagRequired("command")

	removeShortNameCmd.Flags().StringP("name", "n", "", "Short name to remove")
	removeShortNameCmd.MarkFlagRequired("name")
}
