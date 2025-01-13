// common/methods.go

package format

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pterm/pterm"
)

// ConvertServiceName converts service name to endpoint format
// Example:
//
//	cost_analysis -> cost-analysis
func ConvertServiceName(serviceName string) string {
	return strings.ReplaceAll(serviceName, "_", "-")
}

// SetParentHelp customizes the help output for the parent command
func SetParentHelp(cmd *cobra.Command, args []string) {
	cmd.Printf("Usage:\n")
	cmd.Printf("  %s\n", cmd.UseLine())
	cmd.Printf("  %s\n\n", getAlternativeUsage(cmd))

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	printSortedBulletList(cmd, "Verbs")

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s <verb> --help\" for more information about a verb.\n", cmd.CommandPath())
	}
}

// SetVerbHelp customizes the help output for verb commands
func SetVerbHelp(cmd *cobra.Command, args []string) {
	cmd.Printf("Usage:\n  %s\n\n", cmd.UseLine())

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	if resourcesStr, ok := cmd.Annotations["resources"]; ok && resourcesStr != "" {
		resources := strings.Split(resourcesStr, ", ")
		sort.Strings(resources)
		pterm.DefaultSection.Println("Resources")

		listItems := []pterm.BulletListItem{}
		for _, resource := range resources {
			listItems = append(listItems, pterm.BulletListItem{Level: 2, Text: resource})
		}
		pterm.DefaultBulletList.WithItems(listItems).Render()
		cmd.Println()
	}

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s <resource> --help\" for more information about a resource.\n", cmd.CommandPath())
	}
}

// getAlternativeUsage constructs the alternative usage string
func getAlternativeUsage(cmd *cobra.Command) string {
	// Extract the base command without flags
	baseCommand := cmd.CommandPath()
	return fmt.Sprintf("%s <verb> <resource> [flags]", baseCommand)
}

// printSortedBulletList prints a sorted bullet list under a given section title.
func printSortedBulletList(cmd *cobra.Command, sectionTitle string) {
	if len(cmd.Commands()) == 0 {
		return
	}

	pterm.DefaultSection.Println(sectionTitle)

	items := []string{}
	for _, subCmd := range cmd.Commands() {
		if !subCmd.Hidden {
			items = append(items, subCmd.Name())
		}
	}

	sort.Strings(items)

	listItems := make([]pterm.BulletListItem, len(items))
	for i, item := range items {
		listItems[i] = pterm.BulletListItem{Level: 2, Text: item}
	}

	pterm.DefaultBulletList.WithItems(listItems).Render()
	cmd.Println()
}

func RenderTable(data [][]string) {
	// Get terminal width
	terminalWidth := pterm.GetTerminalWidth()

	// Calculate verb column width based on terminal width
	verbColumnWidth := terminalWidth / 2

	// Define alternating colors for better readability
	alternateColors := []pterm.Color{
		pterm.FgDefault,
		pterm.FgYellow,
	}
	currentColorIndex := 0
	previousService := ""

	// Create table with headers
	table := pterm.TableData{{"Service", "Verb", "Resource", "Short Names"}}

	for _, row := range data {
		service := row[0]

		// Switch color if the service name changes
		if service != previousService {
			currentColorIndex = (currentColorIndex + 1) % len(alternateColors)
			previousService = service
		}

		// Apply the current color
		color := alternateColors[currentColorIndex]
		coloredStyle := pterm.NewStyle(color)

		// Color the entire row
		serviceColored := coloredStyle.Sprint(service)
		resourceColored := coloredStyle.Sprint(row[2])
		shortNamesColored := coloredStyle.Sprint(row[3])

		// Split verbs into multiple lines if needed
		verbs := splitIntoLinesWithComma(row[1], verbColumnWidth)
		for i, line := range verbs {
			if i == 0 {
				table = append(table, []string{serviceColored, coloredStyle.Sprint(line), resourceColored, shortNamesColored})
			} else {
				table = append(table, []string{"", coloredStyle.Sprint(line), "", ""})
			}
		}
	}

	// Render the table
	pterm.DefaultTable.WithHasHeader().WithData(table).Render()
}

func splitIntoLinesWithComma(text string, maxWidth int) []string {
	words := strings.Split(text, ", ")
	var lines []string
	var currentLine string

	for _, word := range words {
		if len(currentLine)+len(word)+2 > maxWidth && currentLine != "" {
			lines = append(lines, strings.TrimSuffix(currentLine, ", "))
			currentLine = word
		} else {
			if currentLine != "" {
				currentLine += ", "
			}
			currentLine += word
		}
	}

	if currentLine != "" {
		lines = append(lines, strings.TrimSuffix(currentLine, ", "))
	}

	return lines
}
