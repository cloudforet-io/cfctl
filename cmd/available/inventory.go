// inventory.go

package available

import (
	"fmt"
	"os"

	"github.com/cloudforet-io/cfctl/cmd/common"
	"github.com/spf13/cobra"
)

var InventoryCmd = &cobra.Command{
	Use:     "inventory",
	Short:   "Interact with the Inventory service",
	Long:    `Use this command to interact with the Inventory service.`,
	GroupID: "available",
	Run: func(cmd *cobra.Command, args []string) {
		// If no arguments are provided, display the available verbs
		if len(args) == 0 {
			common.PrintAvailableVerbs(cmd)
			return
		}

		// If arguments are provided, proceed normally
		cmd.Help()
	},
}

func init() {
	InventoryCmd.AddGroup(&cobra.Group{
		ID:    "available",
		Title: "Available Commands:",
	}, &cobra.Group{
		ID:    "other",
		Title: "Other Commands:",
	})

	// Set custom help function using common.CustomParentHelpFunc
	InventoryCmd.SetHelpFunc(common.CustomParentHelpFunc)

	apiResourcesCmd := common.FetchApiResourcesCmd("inventory")
	apiResourcesCmd.GroupID = "available"
	InventoryCmd.AddCommand(apiResourcesCmd)

	err := common.AddVerbCommands(InventoryCmd, "inventory", "available")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding verb commands: %v\n", err)
	}
}
