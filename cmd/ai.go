package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/pterm/pterm"
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
)

var (
	apiToken    string
	configPath  = filepath.Join(os.Getenv("HOME"), ".cfctl", "config")
	resourceDir = filepath.Join(os.Getenv("HOME"), ".cfctl", "training_data") // 학습 전용 디렉터리 경로
)

// aiCmd represents the ai command
var aiCmd = &cobra.Command{
	Use:   "ai",
	Short: "Run AI-powered tasks",
	Long: `Run various AI-powered tasks, including general text processing or natural language
queries using OpenAI's API.`,
	Run: func(cmd *cobra.Command, args []string) {
		inputText, _ := cmd.Flags().GetString("input")
		isNaturalLanguage, _ := cmd.Flags().GetBool("natural")

		if inputText == "" {
			cmd.Help()
			return
		}

		result, err := runAIWithOpenAI(inputText, isNaturalLanguage)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Println("AI Result:", result)
	},
}

// aiConfigCmd represents the ai config command
var aiConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Set up OpenAI Secret Key",
	Run: func(cmd *cobra.Command, args []string) {
		pterm.Info.Println("Setting up OpenAI API key for AI commands...")

		// Prompt user for OpenAI API key
		apiKey, err := pterm.DefaultInteractiveTextInput.WithMultiLine(false).Show("Enter your OpenAI API Key")
		if err != nil {
			pterm.Error.Println("Failed to read API key:", err)
			return
		}

		// Write to config file
		if err := saveAPIKeyToConfig(apiKey); err != nil {
			pterm.Error.Println("Error saving API key:", err)
			return
		}

		pterm.Success.Println("OpenAI API key saved successfully to", configPath)
	},
}

// aiChatCmd represents the ai chat command
var aiChatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Ask questions about project resources",
	Run: func(cmd *cobra.Command, args []string) {
		// Use the query flag instead of args[0]
		chat, err := cmd.Flags().GetString("query")
		if err != nil || chat == "" {
			pterm.Error.Println("Please provide a query with the -q flag.")
			return
		}

		// Load resources context from directory
		contextData, err := loadAPIResourcesContext(resourceDir)
		if err != nil {
			pterm.Error.Println("Failed to load resources context:", err)
			return
		}

		// Call AI function with query and context
		result, err := queryAIWithContext(chat, contextData)
		if err != nil {
			pterm.Error.Println("Error querying AI:", err)
			return
		}

		pterm.Info.Println("AI Response:", result)
	},
}

// runAIWithOpenAI processes input with OpenAI's streaming API
func runAIWithOpenAI(input string, natural bool) (string, error) {
	// Load the API key from config if it's not set in the environment
	apiToken = os.Getenv("OPENAI_API_TOKEN")
	if apiToken == "" {
		var err error
		apiToken, err = readAPIKeyFromConfig()
		if err != nil {
			return "", errors.New("OpenAI API key is not set. Run `cfctl ai config` to configure it.")
		}
	}

	client := openai.NewClient(apiToken)
	ctx := context.Background()

	// Set up the request based on the mode (natural language or standard)
	model := openai.GPT3Babbage002
	if natural {
		model = openai.GPT3Babbage002
	}

	req := openai.CompletionRequest{
		Model:     model,
		MaxTokens: 5,
		Prompt:    input,
		Stream:    true,
	}

	stream, err := client.CreateCompletionStream(ctx, req)
	if err != nil {
		return "", fmt.Errorf("completion stream error: %v", err)
	}
	defer stream.Close()

	// Capture the streamed response
	var responseText string
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			fmt.Println("Stream finished")
			break
		}
		if err != nil {
			return "", fmt.Errorf("stream error: %v", err)
		}
		responseText += response.Choices[0].Text
		fmt.Printf("Stream response: %s", response.Choices[0].Text)
	}
	return responseText, nil
}

// saveAPIKeyToConfig saves or updates the OpenAI API key in the config file
func saveAPIKeyToConfig(apiKey string) error {
	// Read the existing config file content
	content := ""
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("failed to read config file: %v", err)
		}
		content = string(data)
	}

	// Check if OPENAI_SECRET_KEY is already present
	if strings.Contains(content, "OPENAI_SECRET_KEY=") {
		// Update the existing key without adding an extra `=` character
		content = strings.ReplaceAll(content,
			"OPENAI_SECRET_KEY="+getAPIKeyFromContent(content),
			"OPENAI_SECRET_KEY="+apiKey)
	} else {
		// Append the key if not present
		content += "\nOPENAI_SECRET_KEY=" + apiKey
	}

	// Write the updated content back to the config file
	return os.WriteFile(configPath, []byte(content), 0600)
}

// getAPIKeyFromContent extracts the existing API key from content if available
func getAPIKeyFromContent(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "OPENAI_SECRET_KEY=") {
			return line[17:]
		}
	}
	return ""
}

// readAPIKeyFromConfig reads the OpenAI API key from the config file and sets it to apiToken
func readAPIKeyFromConfig() (string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to open config file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line) // 공백 제거
		if strings.HasPrefix(line, "OPENAI_SECRET_KEY=") {
			apiToken = strings.TrimPrefix(line, "OPENAI_SECRET_KEY=")
			apiToken = strings.TrimSpace(apiToken)
			return apiToken, nil
		}
	}
	return "", errors.New("API key not found in config file")
}

// loadAPIResourcesContext loads all files in the given directory and concatenates their content as context
func loadAPIResourcesContext(dirPath string) (string, error) {
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to read resources directory: %v", err)
	}

	var contentBuilder strings.Builder
	for _, file := range files {
		if !file.IsDir() {
			filePath := filepath.Join(dirPath, file.Name())
			data, err := ioutil.ReadFile(filePath)
			if err != nil {
				return "", fmt.Errorf("failed to read file %s: %v", filePath, err)
			}
			contentBuilder.WriteString(string(data) + "\n")
		}
	}

	return contentBuilder.String(), nil
}

// queryAIWithContext queries the OpenAI API with a specific context and user query
func queryAIWithContext(query, contextData string) (string, error) {
	apiToken = os.Getenv("OPENAI_API_TOKEN")
	if apiToken == "" {
		apiToken, _ = readAPIKeyFromConfig()
	}
	if apiToken == "" {
		return "", errors.New("OpenAI API token is not set. Run `cfctl ai config` to configure it.")
	}

	client := openai.NewClient(apiToken)
	ctx := context.Background()

	// Prompt with context for AI model
	prompt := fmt.Sprintf("Context: %s\n\nQuestion: %s\nAnswer:", contextData, query)

	req := openai.CompletionRequest{
		Model:     openai.GPT3Babbage002,
		MaxTokens: 5, // Adjust as needed
		Prompt:    prompt,
		Stream:    false,
	}

	resp, err := client.CreateCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("AI query error: %v", err)
	}

	// Return the AI's response text
	return strings.TrimSpace(resp.Choices[0].Text), nil
}

func init() {
	rootCmd.AddCommand(aiCmd)
	aiCmd.Flags().String("input", "", "Input text for the AI to process")
	aiCmd.Flags().BoolP("natural", "n", false, "Enable natural language mode for the AI")
	aiChatCmd.Flags().StringP("query", "q", "", "Query text for the AI to process")
	aiChatCmd.MarkFlagRequired("query")

	// Add config command as a subcommand to aiCmd
	aiCmd.AddCommand(aiConfigCmd)
	aiCmd.AddCommand(aiChatCmd)
}
