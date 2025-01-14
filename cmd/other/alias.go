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
	"gopkg.in/yaml.v3"
)

var service string

// AliasCmd represents the alias command
var AliasCmd = &cobra.Command{
	Use:   "alias",
	Short: "Manage command aliases",
	Long:  `Manage aliases for frequently used commands.`,
}

var addAliasCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new alias",
	Example: `  $ cfctl alias add -s identity -k user -v "list User"
    
Then use it as:
  $ cfctl identity user     # This command is same as $ cfctl identity list User`,
	Run: func(cmd *cobra.Command, args []string) {
		service, _ := cmd.Flags().GetString("service")
		key, _ := cmd.Flags().GetString("key")
		value, _ := cmd.Flags().GetString("value")

		// Parse command to validate
		parts := strings.Fields(value)
		if len(parts) < 2 {
			pterm.Error.Printf("Invalid command format. Expected '<verb> <resource>', got '%s'\n", value)
			return
		}

		verb := parts[0]
		resource := parts[1]

		if err := validateServiceCommand(service, verb, resource); err != nil {
			pterm.Error.Printf("Invalid command: %v\n", err)
			return
		}

		if err := addAlias(service, key, value); err != nil {
			pterm.Error.Printf("Failed to add alias: %v\n", err)
			return
		}

		pterm.Success.Printf("Successfully added alias '%s' for command '%s' in service '%s'\n", key, value, service)
	},
}

var removeAliasCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove an alias",
	Example: `  # Remove an alias from a specific service
  $ cfctl alias remove -s identity -k user`,
	Run: func(cmd *cobra.Command, args []string) {
		service, _ := cmd.Flags().GetString("service")
		key, _ := cmd.Flags().GetString("key")

		if err := removeAlias(service, key); err != nil {
			pterm.Error.Printf("Failed to remove alias: %v\n", err)
			return
		}

		pterm.Success.Printf("Successfully removed alias '%s' from service '%s'\n", key, service)
	},
}

var listAliasCmd = &cobra.Command{
	Use:   "list",
	Short: "List all aliases",
	Run: func(cmd *cobra.Command, args []string) {
		aliases, err := ListAliases()
		if err != nil {
			pterm.Error.Printf("Failed to list aliases: %v\n", err)
			return
		}

		if len(aliases) == 0 {
			pterm.Info.Println("No aliases found")
			return
		}

		// Create table
		table := pterm.TableData{
			{"Service", "Alias", "Command"},
		}

		// Add aliases to table
		for service, serviceAliases := range aliases {
			if serviceMap, ok := serviceAliases.(map[string]interface{}); ok {
				for alias, command := range serviceMap {
					if cmdStr, ok := command.(string); ok {
						table = append(table, []string{service, alias, cmdStr})
					}
				}
			}
		}

		// Print table
		pterm.DefaultTable.WithHasHeader().WithData(table).Render()
	},
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

	endpoint, _ = transport.GetAPIEndpoint(endpoint)

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
		if row[2] == resource {
			resourceFound = true
			verbs := strings.Split(row[1], ", ")
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

func addAlias(service, key, value string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")

	data, err := os.ReadFile(settingPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read config: %v", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %v", err)
	}

	aliases, ok := config["aliases"].(map[string]interface{})
	if !ok {
		aliases = make(map[string]interface{})
	}

	serviceAliases, ok := aliases[service].(map[string]interface{})
	if !ok {
		serviceAliases = make(map[string]interface{})
	}

	serviceAliases[key] = value
	aliases[service] = serviceAliases

	delete(config, "aliases")

	newData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode config: %v", err)
	}

	aliasData, err := yaml.Marshal(map[string]interface{}{
		"aliases": aliases,
	})
	if err != nil {
		return fmt.Errorf("failed to encode aliases: %v", err)
	}

	finalData := append(newData, aliasData...)

	if err := os.WriteFile(settingPath, finalData, 0644); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

// Function to remove an alias
func removeAlias(service, key string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")

	data, err := os.ReadFile(settingPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %v", err)
	}

	aliases, ok := config["aliases"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no aliases found")
	}

	serviceAliases, ok := aliases[service].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no aliases found for service '%s'", service)
	}

	if _, exists := serviceAliases[key]; !exists {
		return fmt.Errorf("alias '%s' not found in service '%s'", key, service)
	}

	delete(serviceAliases, key)
	if len(serviceAliases) == 0 {
		delete(aliases, service)
	} else {
		aliases[service] = serviceAliases
	}

	config["aliases"] = aliases

	newData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode config: %v", err)
	}

	if err := os.WriteFile(settingPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func ListAliases() (map[string]interface{}, error) {
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

	aliases := v.Get("aliases")
	if aliases == nil {
		return make(map[string]interface{}), nil
	}

	aliasesMap, ok := aliases.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid aliases format")
	}

	return aliasesMap, nil
}

func init() {
	AliasCmd.AddCommand(addAliasCmd)
	AliasCmd.AddCommand(removeAliasCmd)
	AliasCmd.AddCommand(listAliasCmd)

	addAliasCmd.Flags().StringP("service", "s", "", "Service to add alias for")
	addAliasCmd.Flags().StringP("key", "k", "", "Alias key to add")
	addAliasCmd.Flags().StringP("value", "v", "", "Command to execute (e.g., \"list User\")")
	addAliasCmd.MarkFlagRequired("service")
	addAliasCmd.MarkFlagRequired("key")
	addAliasCmd.MarkFlagRequired("value")

	removeAliasCmd.Flags().StringP("service", "s", "", "Service to remove alias from")
	removeAliasCmd.Flags().StringP("key", "k", "", "Alias key to remove")
	removeAliasCmd.MarkFlagRequired("service")
	removeAliasCmd.MarkFlagRequired("key")
}
