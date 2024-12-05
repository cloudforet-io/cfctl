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
	SortBy          string
}

// AddVerbCommands adds subcommands for each verb to the parent command
func AddVerbCommands(parentCmd *cobra.Command, serviceName string, groupID string) error {
	// Build the verb-resource map
	verbResourceMap, err := BuildVerbResourceMap(serviceName)
	if err != nil {
		return nil // Return nil to prevent Cobra from showing additional error messages
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
			Long: fmt.Sprintf(`Supported %d resources for %s command.

%s

%s`,
				len(resources),
				currentVerb,
				pterm.DefaultBox.WithTitle("Interactive Mode").WithTitleTopCenter().Sprint(
					func() string {
						str, _ := pterm.DefaultBulletList.WithItems([]pterm.BulletListItem{
							{Level: 0, Text: "Required parameters will be prompted if not provided"},
							{Level: 0, Text: "Missing parameters will be requested interactively"},
							{Level: 0, Text: "Just follow the prompts to fill in the required fields"},
						}).Srender()
						return str
					}()),
				pterm.DefaultBox.WithTitle("Example").WithTitleTopCenter().Sprint(
					fmt.Sprintf("List resources:\n"+
						"  $ cfctl %s list <Resource>\n\n"+
						"List and sort by field:\n"+
						"  $ cfctl %s list <Resource> -s name\n"+
						"  $ cfctl %s list <Resource> -s created_at\n\n"+
						"Watch for changes:\n"+
						"  $ cfctl %s list <Resource> -w",
						serviceName, serviceName, serviceName, serviceName))),
			Args: cobra.ArbitraryArgs, // Allow any number of arguments
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

				sortBy := ""
				if currentVerb == "list" {
					sortBy, _ = cmd.Flags().GetString("sort")
				}

				options := &FetchOptions{
					Parameters:      parameters,
					JSONParameter:   jsonParameter,
					FileParameter:   fileParameter,
					APIVersion:      apiVersion,
					OutputFormat:    outputFormat,
					CopyToClipboard: copyToClipboard,
					SortBy:          sortBy,
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
			verbCmd.Flags().StringP("sort", "s", "", "Sort by field (e.g. 'name', 'created_at')")
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

		// Update example for list command
		if currentVerb == "list" {
			verbCmd.Long = fmt.Sprintf(`Supported %d resources for %s command.

%s

%s`,
				len(resources),
				currentVerb,
				pterm.DefaultBox.WithTitle("Interactive Mode").WithTitleTopCenter().Sprint(
					func() string {
						str, _ := pterm.DefaultBulletList.WithItems([]pterm.BulletListItem{
							{Level: 0, Text: "Required parameters will be prompted if not provided"},
							{Level: 0, Text: "Missing parameters will be requested interactively"},
							{Level: 0, Text: "Just follow the prompts to fill in the required fields"},
						}).Srender()
						return str
					}()),
				pterm.DefaultBox.WithTitle("Example").WithTitleTopCenter().Sprint(
					fmt.Sprintf("List resources:\n"+
						"  $ cfctl %s list <Resource>\n\n"+
						"List and sort by field:\n"+
						"  $ cfctl %s list <Resource> -s name\n"+
						"  $ cfctl %s list <Resource> -s created_at\n\n"+
						"Watch for changes:\n"+
						"  $ cfctl %s list <Resource> -w",
						serviceName, serviceName, serviceName, serviceName)))
		}

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

	seenItems := make(map[string]bool)

	initialData, err := FetchService(serviceName, verb, resource, &FetchOptions{
		Parameters:      options.Parameters,
		JSONParameter:   options.JSONParameter,
		FileParameter:   options.FileParameter,
		APIVersion:      options.APIVersion,
		OutputFormat:    "",
		CopyToClipboard: false,
	})
	if err != nil {
		return err
	}

	if results, ok := initialData["results"].([]interface{}); ok {
		var recentItems []map[string]interface{}

		for _, item := range results {
			if m, ok := item.(map[string]interface{}); ok {
				identifier := generateIdentifier(m)
				seenItems[identifier] = true

				recentItems = append(recentItems, m)
				if len(recentItems) > 20 {
					recentItems = recentItems[1:]
				}
			}
		}

		if len(recentItems) > 0 {
			fmt.Printf("Recent items:\n")
			printNewItems(recentItems)
		}
	}

	fmt.Printf("\nWatching for changes... (Ctrl+C to quit)\n\n")

	for {
		select {
		case <-ticker.C:
			newData, err := FetchService(serviceName, verb, resource, &FetchOptions{
				Parameters:      options.Parameters,
				JSONParameter:   options.JSONParameter,
				FileParameter:   options.FileParameter,
				APIVersion:      options.APIVersion,
				OutputFormat:    "",
				CopyToClipboard: false,
			})
			if err != nil {
				continue
			}

			var newItems []map[string]interface{}
			if results, ok := newData["results"].([]interface{}); ok {
				for _, item := range results {
					if m, ok := item.(map[string]interface{}); ok {
						identifier := generateIdentifier(m)
						if !seenItems[identifier] {
							newItems = append(newItems, m)
							seenItems[identifier] = true
						}
					}
				}
			}

			if len(newItems) > 0 {
				fmt.Printf("Found %d new items at %s:\n",
					len(newItems),
					time.Now().Format("2006-01-02 15:04:05"))

				printNewItems(newItems)
				fmt.Println()
			}

		case <-sigChan:
			fmt.Println("\nStopping watch...")
			return nil
		}
	}
}

func generateIdentifier(item map[string]interface{}) string {
	if id, ok := item["job_task_id"]; ok {
		return fmt.Sprintf("%v", id)
	}

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

func printNewItems(items []map[string]interface{}) {
	if len(items) == 0 {
		return
	}

	tableData := pterm.TableData{}

	headers := make([]string, 0)
	for key := range items[0] {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	tableData = append(tableData, headers)

	for _, item := range items {
		row := make([]string, len(headers))
		for i, header := range headers {
			if val, ok := item[header]; ok {
				row[i] = formatTableValue(val)
			}
		}
		tableData = append(tableData, row)
	}

	pterm.DefaultTable.WithHasHeader().WithData(tableData).Render()
}
