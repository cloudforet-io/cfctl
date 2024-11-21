// identity.go

package available

import (
	"fmt"
	"os"

	"github.com/cloudforet-io/cfctl/cmd/common"
	"github.com/spf13/cobra"
)

var IdentityCmd = &cobra.Command{
	Use:     "identity",
	Short:   "Interact with the Identity service",
	Long:    `Use this command to interact with the Identity service.`,
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
	IdentityCmd.AddGroup(&cobra.Group{
		ID:    "available",
		Title: "Available Commands:",
	}, &cobra.Group{
		ID:    "other",
		Title: "Other Commands:",
	})

	// Set custom help function using common.CustomHelpFunc
	IdentityCmd.SetHelpFunc(common.CustomVerbHelpFunc)

	apiResourcesCmd.GroupID = "available"
	IdentityCmd.AddCommand(apiResourcesCmd)

	err := common.AddVerbCommands(IdentityCmd, "identity", "other")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding verb commands: %v\n", err)
	}
}

var apiResourcesCmd = &cobra.Command{
	Use:   "api-resources",
	Short: "Displays supported API resources for the Identity service",
	RunE: func(cmd *cobra.Command, args []string) error {
		return common.ListAPIResources("identity")
	},
}
