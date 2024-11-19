// identity.go

package available

import (
	"fmt"
	"os"

	"github.com/cloudforet-io/cfctl/cmd/common"
	"github.com/spf13/cobra"
)

var IdentityCmd = &cobra.Command{
	Use:   "identity",
	Short: "Interact with the Identity service",
	Long:  `Use this command to interact with the Identity service.`,
}

func init() {
	// 1. identity 명령에 그룹 추
	IdentityCmd.AddGroup(&cobra.Group{
		ID:    "available",
		Title: "Available Commands:",
	}, &cobra.Group{
		ID:    "other",
		Title: "Other Commands:",
	})

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
