// common/fetchVerb.go

package common

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pterm/pterm"

	"github.com/spf13/cobra"
)

// FetchOptions holds the flag values for a command
type FetchOptions struct {
	Parameters      []string
	JSONParameter   string
	FileParameter   string
	APIVersion      string
	OutputFormat    string
	CopyToClipboard bool
}

// AddVerbCommands adds subcommands for each verb to the parent command
func AddVerbCommands(parentCmd *cobra.Command, serviceName string, groupID string) error {
	// Build the verb-resource map
	verbResourceMap, err := BuildVerbResourceMap(serviceName)
	if err != nil {
		return fmt.Errorf("failed to build verb-resource map for service %s: %v", serviceName, err)
	}

	// Get a sorted list of verbs
	verbs := make([]string, 0, len(verbResourceMap))
	for verb := range verbResourceMap {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)

	for _, verb := range verbs {
		currentVerb := verb
		resources := verbResourceMap[currentVerb]

		// Prepare Short and Long descriptions
		shortDesc := fmt.Sprintf("Supported %d resources", len(resources))

		verbCmd := &cobra.Command{
			Use:   currentVerb + " <resource>",
			Short: shortDesc,
			Args:  cobra.ArbitraryArgs, // Allow any number of arguments
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) != 1 {
					// Display the help message
					cmd.Help()
					return nil // Do not return an error to prevent additional error messages
				}
				resource := args[0]

				// Retrieve flag values
				parameters, err := cmd.Flags().GetStringArray("parameter")
				if err != nil {
					return err
				}
				jsonParameter, err := cmd.Flags().GetString("json-parameter")
				if err != nil {
					return err
				}
				fileParameter, err := cmd.Flags().GetString("file-parameter")
				if err != nil {
					return err
				}
				apiVersion, err := cmd.Flags().GetString("api-version")
				if err != nil {
					return err
				}
				outputFormat, err := cmd.Flags().GetString("output")
				if err != nil {
					return err
				}
				copyToClipboard, err := cmd.Flags().GetBool("copy")
				if err != nil {
					return err
				}

				options := &FetchOptions{
					Parameters:      parameters,
					JSONParameter:   jsonParameter,
					FileParameter:   fileParameter,
					APIVersion:      apiVersion,
					OutputFormat:    outputFormat,
					CopyToClipboard: copyToClipboard,
				}

				err = FetchService(serviceName, currentVerb, resource, options)
				if err != nil {
					// Use pterm to display the error message
					pterm.Error.Println(err.Error())
					return nil // Return nil to prevent Cobra from displaying its own error message
				}
				return nil
			},
			GroupID: groupID,
			Annotations: map[string]string{
				"resources": strings.Join(resources, ", "),
			},
		}

		// Define flags for verbCmd
		verbCmd.Flags().StringArrayP("parameter", "p", []string{}, "Input Parameter (-p <key>=<value> -p ...)")
		verbCmd.Flags().StringP("json-parameter", "j", "", "JSON type parameter")
		verbCmd.Flags().StringP("file-parameter", "f", "", "YAML file parameter")
		verbCmd.Flags().StringP("api-version", "v", "v1", "API Version")
		verbCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml, json, table, csv)")
		verbCmd.Flags().BoolP("copy", "c", false, "Copy the output to the clipboard (copies any output format)")

		// Set custom help function
		verbCmd.SetHelpFunc(CustomVerbHelpFunc)

		parentCmd.AddCommand(verbCmd)
	}

	return nil
}
