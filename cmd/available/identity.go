package available

import (
	"github.com/cloudforet-io/cfctl/cmd/common"
	"github.com/spf13/cobra"
)

var IdentityCmd = &cobra.Command{
	Use:     "identity <verb> <resource>",
	Short:   "Interact with the Identity service",
	Long:    `Use this command to interact with the Identity service. Available verbs: list, get, create, update, delete, ...`,
	Args:    cobra.ExactArgs(2),
	GroupID: "available",
	RunE: func(cmd *cobra.Command, args []string) error {
		verb := args[0]
		resource := args[1]
		return common.ExecuteCommand("identity", verb, resource)
	},
}

func init() {
	IdentityCmd.AddCommand(apiResourcesCmd)
}

var apiResourcesCmd = &cobra.Command{
	Use:   "api-resources",
	Short: "Displays supported API resources for the Identity service",
	RunE: func(cmd *cobra.Command, args []string) error {
		return common.ListAPIResources("identity")
	},
}
