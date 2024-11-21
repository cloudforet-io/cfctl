// common/methods.go

package common

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// AddVerbCommands adds subcommands for each Verb to the parent command
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
		shortDesc := fmt.Sprintf("Supports %d resources", len(resources))
		longDesc := fmt.Sprintf("Resources:\n  %s", strings.Join(resources, "\n  "))

		verbCmd := &cobra.Command{
			Use:   currentVerb + " <resource>",
			Short: shortDesc,
			Long:  longDesc,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				resource := args[0]
				err := FetchService(serviceName, currentVerb, resource)
				if err != nil {
					return fmt.Errorf("failed to call grpc: %v", err)
				}
				return nil
			},
			GroupID: groupID,
			Annotations: map[string]string{
				"resources": strings.Join(resources, ", "),
			},
		}
		parentCmd.AddCommand(verbCmd)
	}

	return nil
}
