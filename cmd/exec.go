package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"google.golang.org/grpc/credentials/insecure"

	"google.golang.org/grpc/credentials"

	"github.com/golang/protobuf/jsonpb"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"gopkg.in/yaml.v2"
)

var execCmd = &cobra.Command{
	Use:   "exec [verb] [service.resource]",
	Short: "Execute an operation on a resource",
	Long: `Execute an operation on a specified service and resource.

Command format:
	cfctl exec [Verb] [Service].[Resource]

Examples:
	cfctl exec create identity.Role
	cfctl exec list identity.User
	cfctl exec get identity.User -p user_id=user-123
	cfctl exec update identity.Project -f params.yaml`,
	Args: cobra.ExactArgs(2),
	RunE: runExec,
}

func init() {
	rootCmd.AddCommand(execCmd)

	execCmd.Flags().StringArrayP("parameter", "p", []string{}, "Input parameter (-p key=value)")
	execCmd.Flags().StringP("json-parameter", "j", "", "JSON parameter")
	execCmd.Flags().StringP("file-parameter", "f", "", "YAML file parameter")
	execCmd.Flags().StringP("api-version", "v", "v1", "API version")
	execCmd.Flags().StringP("output", "o", "yaml", "Output format (yaml/json)")
}

func runExec(cmd *cobra.Command, args []string) error {
	verb := args[0]
	serviceResource := args[1]

	service, resource, err := parseServiceResource(serviceResource)
	if err != nil {
		return fmt.Errorf("failed to parse service.resource: %w", err)
	}

	parameters, _ := cmd.Flags().GetStringArray("parameter")
	jsonParameter, _ := cmd.Flags().GetString("json-parameter")
	fileParameter, _ := cmd.Flags().GetString("file-parameter")
	apiVersion, _ := cmd.Flags().GetString("api-version")
	output, _ := cmd.Flags().GetString("output")

	params := parseParameters(parameters, jsonParameter, fileParameter)
	return executeAPI(service, resource, verb, params, apiVersion, output)
}

func parseServiceResource(serviceResource string) (string, string, error) {
	parts := strings.Split(serviceResource, ".")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid resource format. It should be [service].[resource]")
	}
	return parts[0], parts[1], nil
}

func parseParameters(parameters []string, jsonParameter string, fileParameter string) map[string]interface{} {
	params := make(map[string]interface{})

	// Handle key=value parameters
	fmt.Println("Command line parameters:", parameters)
	for _, p := range parameters {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) == 2 {
			params[parts[0]] = parts[1]
			fmt.Printf("Added parameter: %s = %v\n", parts[0], parts[1])
		}
	}

	// Handle JSON parameter
	if jsonParameter != "" {
		fmt.Println("JSON parameter:", jsonParameter)
		var jsonParams map[string]interface{}
		if err := json.Unmarshal([]byte(jsonParameter), &jsonParams); err == nil {
			for k, v := range jsonParams {
				params[k] = v
				fmt.Printf("Added JSON parameter: %s = %v\n", k, v)
			}
		} else {
			fmt.Printf("JSON parsing error: %v\n", err)
		}
	}

	// Handle file parameter
	if fileParameter != "" {
		fmt.Printf("Reading file: %s\n", fileParameter)
		fileContent, err := ioutil.ReadFile(fileParameter)
		if err == nil {
			fmt.Printf("File content: %s\n", string(fileContent))
			var fileParams map[string]interface{}
			if err := yaml.Unmarshal(fileContent, &fileParams); err == nil {
				for k, v := range fileParams {
					params[k] = v
					fmt.Printf("Added file parameter: %s = %v\n", k, v)
				}
			} else {
				fmt.Printf("YAML parsing error: %v\n", err)
			}
		} else {
			fmt.Printf("File reading error: %v\n", err)
		}
	}

	fmt.Printf("Final parameters: %+v\n", params)
	return params
}

func getMethodDescriptor(ctx context.Context, conn *grpc.ClientConn, service, resource, method string) (*desc.MethodDescriptor, error) {
	reflectClient := grpcreflect.NewClientV1Alpha(ctx, reflectpb.NewServerReflectionClient(conn))

	// Convert to SpaceONE service namespace format
	// Example: spaceone.api.identity.v1.User
	fullServiceName := fmt.Sprintf("%s.api.dev.spaceone.dev", service)

	fmt.Printf("Looking for service: %s\n", fullServiceName)

	svc, err := reflectClient.ResolveService(fullServiceName)
	if err != nil {
		return nil, fmt.Errorf("service not found %s: %v", fullServiceName, err)
	}

	// Capitalize the first letter of the method name
	methodName := strings.Title(method)
	methodDesc := svc.FindMethodByName(methodName)
	if methodDesc == nil {
		return nil, fmt.Errorf("method not found %s in %s", methodName, fullServiceName)
	}

	return methodDesc, nil
}

func createDynamicMessage(methodDesc *desc.MethodDescriptor, params map[string]interface{}) (*dynamic.Message, error) {
	msgDesc := methodDesc.GetInputType()
	msg := dynamic.NewMessage(msgDesc)

	jsonData, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to convert parameters: %v", err)
	}

	if err := msg.UnmarshalJSON(jsonData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal message: %v", err)
	}

	return msg, nil
}

func callAPI(conn *grpc.ClientConn, service, resource, verb string, params map[string]interface{}) (interface{}, error) {
	ctx := context.Background()

	// Set timeout
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Read token from SpaceONE config file
	cfgFile, err := getEnvironmentConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to find environment config file: %v", err)
	}

	viper.SetConfigFile(cfgFile)
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	// Get token value
	if token := viper.GetString("token"); token != "" {
		md := metadata.New(map[string]string{
			"authorization": "Bearer " + token,
		})
		ctx = metadata.NewOutgoingContext(ctx, md)
	} else {
		return nil, fmt.Errorf("token is not set")
	}

	// Get method descriptor using reflection
	methodDesc, err := getMethodDescriptor(ctx, conn, service, resource, verb)
	if err != nil {
		return nil, err
	}

	// Create dynamic message
	msg, err := createDynamicMessage(methodDesc, params)
	if err != nil {
		return nil, err
	}

	// Create dynamic gRPC client
	stub := grpcdynamic.NewStub(conn)

	// Invoke API
	resp, err := stub.InvokeRpc(ctx, methodDesc, msg)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %v", err)
	}

	// Handle response
	if dynamicMsg, ok := resp.(*dynamic.Message); ok {
		jsonMarshaler := &jsonpb.Marshaler{
			EmitDefaults: true,
			OrigName:     true,
			Indent:       "  ",
		}
		jsonStr, err := jsonMarshaler.MarshalToString(dynamicMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert response: %v", err)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %v", err)
		}
		return result, nil
	}

	return resp, nil
}

func executeAPI(service, resource, verb string, params map[string]interface{}, apiVersion, output string) error {
	spinner, _ := pterm.DefaultSpinner.Start("Executing API call...")

	// Read SpaceONE config file
	cfgFile, err := getEnvironmentConfig()
	if err != nil {
		spinner.Fail(fmt.Sprintf("failed to find environment config file: %v", err))
		return err
	}

	viper.SetConfigFile(cfgFile)
	if err := viper.ReadInConfig(); err != nil {
		spinner.Fail(fmt.Sprintf("failed to read config file: %v", err))
		return err
	}

	endpointsMap := viper.GetStringMapString("endpoints")
	endpoint, ok := endpointsMap[service]
	if !ok {
		spinner.Fail(fmt.Sprintf("failed to find endpoint for service %s", service))
		return fmt.Errorf("endpoint not found for service: %s", service)
	}

	// Parse endpoint
	parts := strings.Split(endpoint, "://")
	if len(parts) != 2 {
		return fmt.Errorf("invalid endpoint format: %s", endpoint)
	}

	scheme := parts[0]
	hostPort := strings.SplitN(parts[1], "/", 2)[0]

	var opts []grpc.DialOption
	if scheme == "grpc+ssl" {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	fmt.Printf("Connecting to endpoint: %s\n", hostPort)

	conn, err := grpc.Dial(hostPort, opts...)
	if err != nil {
		spinner.Fail(fmt.Sprintf("failed to connect to server: %v", err))
		return err
	}
	defer conn.Close()

	client := grpc_reflection_v1alpha.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(context.Background())
	if err != nil {
		return fmt.Errorf("failed to create reflection client: %v", err)
	}

	// Construct service name
	fullServiceName := fmt.Sprintf("spaceone.api.%s.v1.%s", service, strings.Title(resource))

	// Request method information
	req := &grpc_reflection_v1alpha.ServerReflectionRequest{
		MessageRequest: &grpc_reflection_v1alpha.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: fullServiceName,
		},
	}

	if err := stream.Send(req); err != nil {
		return fmt.Errorf("failed to send reflection request: %v", err)
	}

	response, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive reflection response: %v", err)
	}

	spinner.Success("API call complete")

	// Handle output format
	var outputData []byte
	if output == "yaml" {
		outputData, err = yaml.Marshal(response)
	} else if output == "json" {
		outputData, err = json.MarshalIndent(response, "", "  ")
	} else {
		return fmt.Errorf("unsupported output format: %s", output)
	}

	if err != nil {
		return fmt.Errorf("failed to format output: %v", err)
	}

	fmt.Println(string(outputData))
	return nil
}
