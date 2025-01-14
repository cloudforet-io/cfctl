/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package other

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cloudforet-io/cfctl/pkg/transport"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type ResourceSpec struct {
	Service  string                 `yaml:"service"`
	Verb     string                 `yaml:"verb"`
	Resource string                 `yaml:"resource"`
	Spec     map[string]interface{} `yaml:"spec"`
}

// ApplyCmd represents the apply command
var ApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a configuration to a resource using a file",
	Long:  `Apply the configuration in the YAML file to create or update a resource`,
	Example: `  # Create test.yaml
  service: identity
  verb: create
  resource: user
  spec:
    user_id: test-user
    auth_type: LOCAL

  # Apply the configuration in test.yaml
  $ cfctl apply -f test.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filename, _ := cmd.Flags().GetString("filename")
		if filename == "" {
			return fmt.Errorf("filename is required (-f flag)")
		}

		// Read and parse YAML file
		data, err := os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("failed to read file: %v", err)
		}

		var resource ResourceSpec
		if err := yaml.Unmarshal(data, &resource); err != nil {
			return fmt.Errorf("failed to parse YAML: %v", err)
		}

		// Convert spec to parameters
		var parameters []string
		for key, value := range resource.Spec {
			switch v := value.(type) {
			case string:
				parameters = append(parameters, fmt.Sprintf("%s=%s", key, v))
			case bool, int, float64:
				parameters = append(parameters, fmt.Sprintf("%s=%v", key, v))
			case []interface{}, map[string]interface{}:
				// For arrays and maps, convert to JSON string
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					return fmt.Errorf("failed to marshal parameter %s: %v", key, err)
				}
				parameters = append(parameters, fmt.Sprintf("%s=%s", key, string(jsonBytes)))
			default:
				// For other complex types, try JSON marshaling
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					return fmt.Errorf("failed to marshal parameter %s: %v", key, err)
				}
				parameters = append(parameters, fmt.Sprintf("%s=%s", key, string(jsonBytes)))
			}
		}

		options := &transport.FetchOptions{
			Parameters: parameters,
		}

		_, err = transport.FetchService(resource.Service, resource.Verb, resource.Resource, options)
		if err != nil {
			pterm.Error.Println(err.Error())
			return nil
		}

		pterm.Success.Printf("Resource %s/%s applied successfully\n", resource.Service, resource.Resource)
		return nil
	},
}

func init() {
	ApplyCmd.Flags().StringP("filename", "f", "", "Filename to use to apply the resource")
	ApplyCmd.MarkFlagRequired("filename")
}
