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

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// IdentityCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	//IdentityCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
