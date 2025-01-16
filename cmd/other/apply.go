package other

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

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

func parseResourceSpecs(data []byte) ([]ResourceSpec, error) {
	var resources []ResourceSpec

	// Split YAML documents
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var resource ResourceSpec
		if err := decoder.Decode(&resource); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to parse YAML: %v", err)
		}
		resources = append(resources, resource)
	}

	return resources, nil
}

// ApplyCmd represents the apply command
var ApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a configuration to a resource using a file",
	Long:  `Apply the configuration in the YAML file to create or update a resource`,
	Example: `  # 01. Create a test.yaml file with service-verb-resource-spec format
  service: identity
  verb: create
  resource: WorkspaceGroup
  spec:
    name: Test Workspace Group
  ---
  service: identity
  verb: add_users
  resource: WorkspaceGroup
  spec:
    workspace_group_id: wg-12345
    users:
      - user_id: u-123
        role_id: role-123
      - user_id: u-456
        role_id: role-456

  # 02. Apply the configuration
  cfctl apply -f test.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filename, _ := cmd.Flags().GetString("filename")
		if filename == "" {
			return fmt.Errorf("filename is required (-f flag)")
		}

		// Read YAML file
		data, err := os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("failed to read file: %v", err)
		}

		// Parse all resource specs
		resources, err := parseResourceSpecs(data)
		if err != nil {
			return err
		}

		// Process each resource sequentially
		var lastResponse map[string]interface{}
		for i, resource := range resources {
			pterm.Info.Printf("Applying resource %d/%d: %s/%s\n",
				i+1, len(resources), resource.Service, resource.Resource)

			// Convert spec to parameters
			parameters := convertSpecToParameters(resource.Spec, lastResponse)

			options := &transport.FetchOptions{
				Parameters: parameters,
			}

			response, err := transport.FetchService(resource.Service, resource.Verb, resource.Resource, options)
			if err != nil {
				pterm.Error.Printf("Failed to apply resource %d/%d: %v\n", i+1, len(resources), err)
				return err
			}

			lastResponse = response
			pterm.Success.Printf("Resource %d/%d applied successfully\n", i+1, len(resources))
		}

		return nil
	},
}

func convertSpecToParameters(spec map[string]interface{}, lastResponse map[string]interface{}) []string {
	var parameters []string

	for key, value := range spec {
		switch v := value.(type) {
		case string:
			// Check if value references previous response
			if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
				refPath := strings.Trim(v, "${}")
				if val := getValueFromPath(lastResponse, refPath); val != "" {
					parameters = append(parameters, fmt.Sprintf("%s=%s", key, val))
				}
			} else {
				parameters = append(parameters, fmt.Sprintf("%s=%s", key, v))
			}
		case []interface{}, map[string]interface{}:
			jsonBytes, err := json.Marshal(v)
			if err == nil {
				parameters = append(parameters, fmt.Sprintf("%s=%s", key, string(jsonBytes)))
			}
		default:
			parameters = append(parameters, fmt.Sprintf("%s=%v", key, v))
		}
	}

	return parameters
}

func getValueFromPath(data map[string]interface{}, path string) string {
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if v, ok := current[part]; ok {
			switch val := v.(type) {
			case map[string]interface{}:
				current = val
			case string:
				return val
			default:
				if str, err := json.Marshal(val); err == nil {
					return string(str)
				}
				return fmt.Sprintf("%v", val)
			}
		} else {
			return ""
		}
	}

	return ""
}

func init() {
	ApplyCmd.Flags().StringP("filename", "f", "", "Filename to use to apply the resource")
	ApplyCmd.MarkFlagRequired("filename")
}
