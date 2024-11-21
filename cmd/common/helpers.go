// common/methods.go

package common

import (
	"context"
	"crypto/tls"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mattn/go-runewidth"

	"github.com/pterm/pterm"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"

	"github.com/jhump/protoreflect/grpcreflect"
)

// BuildVerbResourceMap builds a mapping from verbs to resources for a given service
func BuildVerbResourceMap(serviceName string) (map[string][]string, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %v", err)
	}

	var envPrefix string
	if strings.HasPrefix(config.Environment, "dev-") {
		envPrefix = "dev"
	} else if strings.HasPrefix(config.Environment, "stg-") {
		envPrefix = "stg"
	} else {
		return nil, fmt.Errorf("unsupported environment prefix")
	}
	hostPort := fmt.Sprintf("%s.api.%s.spaceone.dev:443", serviceName, envPrefix)

	// Configure gRPC connection
	var opts []grpc.DialOption
	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
	}
	creds := credentials.NewTLS(tlsConfig)
	opts = append(opts, grpc.WithTransportCredentials(creds))

	// Establish the connection
	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		return nil, fmt.Errorf("connection failed: unable to connect to %s: %v", hostPort, err)
	}
	defer conn.Close()

	ctx := metadata.AppendToOutgoingContext(context.Background(), "token", config.Environments[config.Environment].Token)
	refClient := grpcreflect.NewClient(ctx, grpc_reflection_v1alpha.NewServerReflectionClient(conn))
	defer refClient.Reset()

	// List all services
	services, err := refClient.ListServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %v", err)
	}

	verbResourceMap := make(map[string]map[string]struct{})

	for _, s := range services {
		if strings.HasPrefix(s, "grpc.reflection.") {
			continue
		}

		if !strings.Contains(s, fmt.Sprintf(".%s.", serviceName)) {
			continue
		}

		serviceDesc, err := refClient.ResolveService(s)
		if err != nil {
			continue
		}

		// Extract the resource name from the service name
		parts := strings.Split(s, ".")
		resourceName := parts[len(parts)-1]

		for _, method := range serviceDesc.GetMethods() {
			verb := method.GetName()
			if verbResourceMap[verb] == nil {
				verbResourceMap[verb] = make(map[string]struct{})
			}
			verbResourceMap[verb][resourceName] = struct{}{}
		}
	}

	// Convert the map of resources to slices
	result := make(map[string][]string)
	for verb, resourcesSet := range verbResourceMap {
		resources := []string{}
		for resource := range resourcesSet {
			resources = append(resources, resource)
		}
		sort.Strings(resources)
		result[verb] = resources
	}

	return result, nil
}

// CustomParentHelpFunc customizes the help output for the parent command
func CustomParentHelpFunc(cmd *cobra.Command, args []string) {
	cmd.Printf("Usage:\n")
	cmd.Printf("  %s\n", cmd.UseLine())
	cmd.Printf("  %s\n\n", getAlternativeUsage(cmd))

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	if len(cmd.Commands()) > 0 {
		pterm.DefaultSection.Println("Verbs")
		verbs := []string{}
		for _, verbCmd := range cmd.Commands() {
			if !verbCmd.Hidden {
				verbs = append(verbs, verbCmd.Name())
			}
		}
		sort.Strings(verbs)
		for _, verb := range verbs {
			pterm.Println(fmt.Sprintf("  • %s", verb))
		}
		cmd.Println()
	}

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s [verb] --help\" for more information about a verb.\n", cmd.CommandPath())
	}
}

// PrintAvailableVerbs prints the available verbs along with both usage patterns
func PrintAvailableVerbs(cmd *cobra.Command) {
	// Usage 섹션 출력
	cmd.Printf("Usage:\n")
	cmd.Printf("  %s\n", cmd.UseLine())
	cmd.Printf("  %s\n\n", getAlternativeUsage(cmd))

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
		cmd.Println()
	}

	verbs := []string{}
	for _, verbCmd := range cmd.Commands() {
		if !verbCmd.Hidden {
			verbs = append(verbs, verbCmd.Name())
		}
	}
	sort.Strings(verbs)

	pterm.Printf("Supports %d verbs\n\n", len(verbs))

	pterm.DefaultSection.Println("Verbs")
	listItems := []pterm.BulletListItem{}
	for _, verb := range verbs {
		listItems = append(listItems, pterm.BulletListItem{Level: 2, Text: verb})
	}
	pterm.DefaultBulletList.WithItems(listItems).Render()
	cmd.Println()

	cmd.Println("Flags:")
	cmd.Print(cmd.Flags().FlagUsages())
	cmd.Println()

	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s [verb] --help\" for more information about a verb.\n", cmd.CommandPath())
	}
}

// RenderTable renders a table with given headers and data.
func RenderTable(headers []string, data [][]string) {
	// Calculate the terminal width
	terminalWidth, _, err := pterm.GetTerminalSize()
	if err != nil {
		terminalWidth = 80 // Default width if unable to get terminal size
	}

	// Define minimum column widths
	minColumnWidths := make([]int, len(headers))
	for i := range minColumnWidths {
		minColumnWidths[i] = runewidth.StringWidth(headers[i]) + 2 // Minimum width based on header length
	}

	// Adjust column widths based on content
	columnWidths := make([]int, len(headers))
	copy(columnWidths, minColumnWidths)

	for _, row := range data {
		for i, cell := range row {
			cellLines := strings.Split(cell, "\n")
			for _, line := range cellLines {
				lineWidth := runewidth.StringWidth(line) + 2
				if lineWidth > columnWidths[i] {
					columnWidths[i] = lineWidth
				}
			}
		}
	}

	// Calculate total width
	totalWidth := len(headers) - 1 // Spaces between columns
	for _, w := range columnWidths {
		totalWidth += w
	}

	// If total width exceeds terminal width, reduce column widths
	if totalWidth > terminalWidth {
		availableWidth := terminalWidth - (len(headers) - 1)
		// Distribute available width proportionally
		totalMinWidth := 0
		for _, w := range minColumnWidths {
			totalMinWidth += w
		}
		if totalMinWidth > availableWidth {
			// Set all columns to minimum widths
			copy(columnWidths, minColumnWidths)
		} else {
			extraWidth := availableWidth - totalMinWidth
			for i := range columnWidths {
				columnWidths[i] = minColumnWidths[i] + (extraWidth * minColumnWidths[i] / totalMinWidth)
			}
		}
	}

	// Build the table data
	tableData := pterm.TableData{}
	tableData = append(tableData, headers)

	for _, row := range data {
		wrappedRow := make([]string, len(row))
		for i, cell := range row {
			wrappedRow[i] = resourceWordWrap(cell, columnWidths[i]-2) // Subtract 2 for padding
		}
		tableData = append(tableData, wrappedRow)
	}

	// Render the table using pterm with alternate row styling
	pterm.DefaultTable.
		WithHasHeader().
		WithBoxed(true).
		WithData(tableData).
		WithLeftAlignment().
		Render()
}

// CustomVerbHelpFunc customizes the help output for verb commands
func CustomVerbHelpFunc(cmd *cobra.Command, args []string) {
	cmd.Printf("Usage:\n  %s\n\n", cmd.UseLine())

	if cmd.Short != "" {
		cmd.Println(cmd.Short)
	}

	if resourcesStr, ok := cmd.Annotations["resources"]; ok && resourcesStr != "" {
		resources := strings.Split(resourcesStr, ", ")
		sort.Strings(resources)
		pterm.DefaultSection.Print("Resources")
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
	// 추가적인 도움말 안내
	if len(cmd.Commands()) > 0 {
		cmd.Printf("Use \"%s [resource] --help\" for more information about a resource.\n", cmd.CommandPath())
	}
}

// getAlternativeUsage constructs the alternative usage string
func getAlternativeUsage(cmd *cobra.Command) string {
	// Extract the base command without flags
	baseCommand := cmd.CommandPath()
	return fmt.Sprintf("%s [verb] <resource> [flags]", baseCommand)
}

// resourceWordWrap function remains the same
func resourceWordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var wrappedText string
	var line string
	words := strings.Fields(text)

	for _, word := range words {
		wordWidth := runewidth.StringWidth(word)
		lineWidth := runewidth.StringWidth(line)
		if lineWidth+wordWidth+1 > width {
			if wrappedText != "" {
				wrappedText += "\n"
			}
			wrappedText += line
			line = word
		} else {
			if line != "" {
				line += " "
			}
			line += word
		}
	}
	if line != "" {
		if wrappedText != "" {
			wrappedText += "\n"
		}
		wrappedText += line
	}

	return wrappedText
}
