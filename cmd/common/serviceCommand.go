package common

import (
	"fmt"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

// CreateServiceCommand creates a new cobra command for a service
func CreateServiceCommand(serviceName string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [flags]\n  %s <verb> <resource> [flags]", serviceName, serviceName),
		Short: fmt.Sprintf("Interact with the %s service", serviceName),
		RunE: func(cmd *cobra.Command, args []string) error {
			// If no args provided, show help
			if len(args) == 0 {
				return cmd.Help()
			}

			// Check if the first argument is a short name
			if actualVerb, actualResource, isShortName := ResolveShortName(serviceName, args[0]); isShortName {
				// Replace the short name with actual verb and resource
				args = append([]string{actualVerb, actualResource}, args[1:]...)
			}

			// After resolving short name, proceed with normal command processing
			if len(args) < 2 {
				return cmd.Help()
			}

			verb := args[0]
			resource := args[1]

			// Create options from remaining args
			options := &FetchOptions{
				Parameters: make([]string, 0),
			}

			// Process remaining args as parameters
			for i := 2; i < len(args); i++ {
				if strings.HasPrefix(args[i], "--") {
					paramName := strings.TrimPrefix(args[i], "--")
					if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
						options.Parameters = append(options.Parameters, fmt.Sprintf("%s=%s", paramName, args[i+1]))
						i++
					}
				}
			}

			// Call FetchService with the processed arguments
			result, err := FetchService(serviceName, verb, resource, options)
			if err != nil {
				pterm.Error.Printf("Failed to execute command: %v\n", err)
				return err
			}

			if result != nil {
				// The result will be printed by FetchService if needed
				return nil
			}

			return nil
		},
	}

	return cmd
} 
