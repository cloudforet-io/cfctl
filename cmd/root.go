package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string
var endpoint string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cfctl",
	Short: "cfctl controls the SpaceONE services.",
	Long: `cfctl controls the SpaceONE services.

  Find more information at: https://docs.spaceone.megazone.io/cfctl`,
	Run: func(cmd *cobra.Command, args []string) {
		runEvans()
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		pterm.Error.Println("Error executing root command:", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.spaceone/cfctl.yaml)")
	rootCmd.PersistentFlags().StringVarP(&endpoint, "endpoint", "e", "", "endpoint to use for the command (e.g., identity, inventory)")
	rootCmd.MarkPersistentFlagRequired("endpoint")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	//rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.AddConfigPath(filepath.Join(home, ".spaceone"))
		viper.SetConfigName("cfctl")
		viper.SetConfigType("yaml")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		pterm.Info.Println("Using config file:", viper.ConfigFileUsed())
	} else {
		pterm.Warning.Println("No valid config file found.")
	}
}

// runEvans executes the Evans command with the required parameters.
func runEvans() {
	token := viper.GetString("token")
	if token == "" {
		pterm.Error.Println("Error: token not found in config file")
		os.Exit(1)
	}

	// Find the specified endpoint
	var host string
	endpoints := viper.GetStringMapString("endpoints")
	if endpointURL, exists := endpoints[endpoint]; exists {
		parts := strings.Split(endpointURL, "//")
		if len(parts) > 1 {
			host = strings.Split(parts[1], ":")[0]
		}
	}

	if host == "" {
		pterm.Error.Printf("Error: endpoint '%s' not found or invalid in config file\n", endpoint)
		os.Exit(1)
	}

	cmd := exec.Command("evans", "--reflection", "repl", "-p", "443", "--tls", "--header", fmt.Sprintf("token=%s", token), "--host", host)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	err := cmd.Run()
	if err != nil {
		pterm.Error.Printf("Error running Evans: %v\n", err)
		os.Exit(1)
	}
}
