// Package other /*
package other

import (
	"github.com/spf13/cobra"
)

// SettingCmd represents the setting command
var SettingCmd = &cobra.Command{
	Use:   "setting",
	Short: "Manage cfctl setting file ($HOME/.cfctl/setting.yaml)",
	Long: `Manage setting file for cfctl. You can initialize,
switch environments, and display the current configuration.`,
	Run: func(cmd *cobra.Command, args []string) {},
}

func init() {
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// settingCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// settingCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
