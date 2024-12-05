package other

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ShortNameCmd represents the shortName command
var ShortNameCmd = &cobra.Command{
	Use:   "short_name",
	Short: "Manage short names for commands",
	Long:  `Manage short names for frequently used commands.`,
}

var addShortNameCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new short name",
	Example: `  $ cfctl short_name add -n user -c "identity list User"

  Then use them as:
  $ cfctl user     # This command is same as $ cfctl identity list User`,
	Run: func(cmd *cobra.Command, args []string) {
		// Show example if no flags are provided
		if !cmd.Flags().Changed("name") || !cmd.Flags().Changed("command") {
			pterm.DefaultBox.
				WithTitle("Short Name Examples").
				WithTitleTopCenter().
				WithBoxStyle(pterm.NewStyle(pterm.FgLightBlue)).
				Println(`Example:
  $ cfctl short_name add -n user -c "identity list User"

Then use them as:
  $ cfctl user     # This command is same as $ cfctl identity list User`)
			return
		}

		shortName, _ := cmd.Flags().GetString("name")
		command, _ := cmd.Flags().GetString("command")

		if err := addShortName(shortName, command); err != nil {
			pterm.Error.Printf("Failed to add short name: %v\n", err)
			return
		}

		pterm.Success.Printf("Successfully added short name '%s' for command '%s'\n", shortName, command)
	},
}

var removeShortNameCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a short name",
	Run: func(cmd *cobra.Command, args []string) {
		shortName, err := cmd.Flags().GetString("name")
		if err != nil || shortName == "" {
			pterm.Error.Println("The --name (-n) flag is required")
			cmd.Help()
			return
		}

		if err := removeShortName(shortName); err != nil {
			pterm.Error.Printf("Failed to remove short name: %v\n", err)
			return
		}

		pterm.Success.Printf("Successfully removed short name '%s'\n", shortName)
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
			{"Short Name", "Command"},
		}

		// Add short names to table
		for name, command := range shortNames {
			table = append(table, []string{name, command})
		}

		// Print table
		pterm.DefaultTable.WithHasHeader().WithData(table).Render()
	},
}

func addShortName(shortName, command string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.toml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read config: %v", err)
	}

	v.Set(fmt.Sprintf("short_names.%s", shortName), command)

	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func removeShortName(shortName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.toml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	// Check if short name exists
	if !v.IsSet(fmt.Sprintf("short_names.%s", shortName)) {
		return fmt.Errorf("short name '%s' not found", shortName)
	}

	// Get all short names
	shortNames := v.GetStringMap("short_names")
	delete(shortNames, shortName)

	// Update config with removed short name
	v.Set("short_names", shortNames)

	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func listShortNames() (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.toml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	shortNames := v.GetStringMapString("short_names")
	if shortNames == nil {
		return make(map[string]string), nil
	}

	return shortNames, nil
}

func init() {
	ShortNameCmd.AddCommand(addShortNameCmd)
	ShortNameCmd.AddCommand(removeShortNameCmd)
	ShortNameCmd.AddCommand(listShortNameCmd)
	addShortNameCmd.Flags().StringP("name", "n", "", "Short name to add")
	addShortNameCmd.Flags().StringP("command", "c", "", "Command to execute")
	addShortNameCmd.MarkFlagRequired("name")
	addShortNameCmd.MarkFlagRequired("command")
	removeShortNameCmd.Flags().StringP("name", "n", "", "Short name to remove")
	removeShortNameCmd.MarkFlagRequired("name")
}
