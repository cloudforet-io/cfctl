package other

import (
	"strings"

	"github.com/cloudforet-io/cfctl/pkg/configs"
	"github.com/cloudforet-io/cfctl/pkg/format"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

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

		if err := format.ValidateServiceCommand(service, verb, resource); err != nil {
			pterm.Error.Printf("Invalid command: %v\n", err)
			return
		}

		if err := configs.AddAlias(service, key, value); err != nil {
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

		if err := configs.RemoveAlias(service, key); err != nil {
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
		aliases, err := configs.ListAliases()
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
