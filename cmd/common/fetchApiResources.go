// fetchApiResources.go

package common

import (
	"fmt"

	"github.com/spf13/cobra"
)

// FetchApiResourcesCmd provides api-resources command for the given service
func FetchApiResourcesCmd(serviceName string) *cobra.Command {
	return &cobra.Command{
		Use:   "api_resources",
		Short: fmt.Sprintf("Displays supported API resources for the %s service", serviceName),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ListAPIResources(serviceName)
		},
	}
}
