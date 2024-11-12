package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pterm/pterm"
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/cobra"
)

var (
	apiToken   string
	configPath = filepath.Join(os.Getenv("HOME"), ".spaceone", "config")
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

// runAIWithOpenAI processes input with OpenAI's streaming API
func runAIWithOpenAI(input string, natural bool) (string, error) {
	// Load the API key from config if it's not set in the environment
	apiToken = os.Getenv("OPENAI_API_TOKEN")
	if apiToken == "" {
		apiToken, _ = readAPIKeyFromConfig()
	}
	if apiToken == "" {
		return "", errors.New("OpenAI API key is not set. Run `cfctl ai config` to configure it.")
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
		// Update the existing key
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

// readAPIKeyFromConfig reads the OpenAI API key from the config file
func readAPIKeyFromConfig() (string, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 0 && line[0:17] == "OPENAI_SECRET_KEY=" {
			return line[17:], nil
		}
	}
	return "", errors.New("API key not found in config file")
}

func init() {
	rootCmd.AddCommand(aiCmd)
	aiCmd.Flags().String("input", "", "Input text for the AI to process")
	aiCmd.Flags().BoolP("natural", "n", false, "Enable natural language mode for the AI")

	// Add config command as a subcommand to aiCmd
	aiCmd.AddCommand(aiConfigCmd)
}
