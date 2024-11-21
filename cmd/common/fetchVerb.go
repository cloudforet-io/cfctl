// common/fetchVerb.go

package common

import (
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

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

				if currentVerb == "list" && !cmd.Flags().Changed("output") {
					options.OutputFormat = "table"
				}

				watch, _ := cmd.Flags().GetBool("watch")
				if watch && currentVerb == "list" {
					return watchResource(serviceName, currentVerb, resource, options)
				}

				_, err = FetchService(serviceName, currentVerb, resource, options)
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

		if currentVerb == "list" {
			verbCmd.Flags().BoolP("watch", "w", false, "Watch for changes")
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

// watchResource monitors a resource for changes and prints updates
func watchResource(serviceName, verb, resource string, options *FetchOptions) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	// Map to store seen results
	seenResults := make(map[string]bool)

	// Create a copy of options for initial fetch
	initialOptions := *options

	// Fetch and display initial data
	initialData, err := FetchService(serviceName, verb, resource, &initialOptions)
	if err != nil {
		return err
	}

	// Process initial results
	if results, ok := initialData["results"].([]interface{}); ok {
		for _, item := range results {
			if m, ok := item.(map[string]interface{}); ok {
				identifier := generateIdentifier(m)
				seenResults[identifier] = true
			}
		}
	}

	fmt.Printf("\nWatching for changes... (Ctrl+C to quit)\n")

	// Create options for subsequent fetches without output
	watchOptions := *options
	watchOptions.OutputFormat = ""

	for {
		select {
		case <-ticker.C:
			newData, err := FetchService(serviceName, verb, resource, &watchOptions)
			if err != nil {
				continue
			}

			if results, ok := newData["results"].([]interface{}); ok {
				newItems := []map[string]interface{}{}

				for _, item := range results {
					if m, ok := item.(map[string]interface{}); ok {
						identifier := generateIdentifier(m)
						if !seenResults[identifier] {
							seenResults[identifier] = true
							newItems = append(newItems, m)
						}
					}
				}

				if len(newItems) > 0 {
					printNewItems(newItems)
				}
			}

		case <-sigChan:
			fmt.Println("\nStopping watch...")
			return nil
		}
	}
}

// generateIdentifier creates a unique identifier for an item based on its contents
func generateIdentifier(item map[string]interface{}) string {
	var keys []string
	for k := range item {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%v=%v", k, item[k]))
	}
	return strings.Join(parts, ",")
}

// printNewItems displays new items in table format
func printNewItems(items []map[string]interface{}) {
	if len(items) == 0 {
		return
	}

	// Prepare table data
	tableData := pterm.TableData{}

	// Extract headers from first item
	headers := make([]string, 0)
	for key := range items[0] {
		headers = append(headers, key)
	}
	sort.Strings(headers)

	// Convert each item to a table row
	for _, item := range items {
		row := make([]string, len(headers))
		for i, header := range headers {
			if val, ok := item[header]; ok {
				row[i] = formatTableValue(val)
			}
		}
		tableData = append(tableData, row)
	}

	// Render the table
	pterm.DefaultTable.WithData(tableData).Render()
}
