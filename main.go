package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	openai "github.com/sashabaranov/go-openai"
	"github.com/joho/godotenv"
)

func main() {
	inputFile := flag.String("input-file", "", "read input content from file")
	inputText := flag.String("input", "", "input text directly")
	stdin := flag.Bool("stdin", false, "read input content from stdin (pipe)")
	promptText := flag.String("prompt", "", "prompt text")
	promptFile := flag.String("prompt-file", "", "read prompt from file")
	configPath := flag.String("config", "", "config file path (default: .env)")
	flag.Parse()

	// validate prompt: exactly one required
	promptCount := 0
	if *promptText != "" {
		promptCount++
	}
	if *promptFile != "" {
		promptCount++
	}
	if promptCount != 1 {
		fmt.Fprintln(os.Stderr, "Error: exactly one of --prompt, --prompt-file is required")
		fmt.Fprintln(os.Stderr, "Usage: llm_prompt_cli --prompt <text> [--input <text> | --input-file <path> | --stdin] [--config <path>]")
		fmt.Fprintln(os.Stderr, "       llm_prompt_cli --prompt-file <path> [--input <text> | --input-file <path> | --stdin] [--config <path>]")
		os.Exit(1)
	}

	// validate input: optional, at most one
	inputCount := 0
	if *inputFile != "" {
		inputCount++
	}
	if *inputText != "" {
		inputCount++
	}
	if *stdin {
		inputCount++
	}
	if inputCount > 1 {
		fmt.Fprintln(os.Stderr, "Error: at most one of --input, --input-file, --stdin is allowed")
		os.Exit(1)
	}

	// load config
	envFile := ".env"
	if *configPath != "" {
		envFile = *configPath
	}
	if err := godotenv.Load(envFile); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load config file %s: %v\n", envFile, err)
	}

	baseURL := os.Getenv("baseURL")
	apiKey := os.Getenv("apiKey")
	model := os.Getenv("model")

	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: apiKey is not set. Check your config file.")
		os.Exit(1)
	}
	if baseURL == "" {
		fmt.Fprintln(os.Stderr, "Error: baseURL is not set. Check your config file.")
		os.Exit(1)
	}
	if model == "" {
		model = "glm-4.7"
	}

	customHeaders := parseEnvHeaders(os.Getenv("headers"))

	// read prompt
	var prompt string
	var err error
	switch {
	case *promptText != "":
		prompt = *promptText
	case *promptFile != "":
		prompt, err = readFile(*promptFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading prompt file %s: %v\n", *promptFile, err)
			os.Exit(1)
		}
	}

	// read input (optional)
	var input string
	switch {
	case *inputFile != "":
		input, err = readFile(*inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading input file %s: %v\n", *inputFile, err)
			os.Exit(1)
		}
	case *inputText != "":
		input = *inputText
	case *stdin:
		input, err = readStdin()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
	}

	// combine: if input exists, append after prompt
	fullPrompt := prompt
	if input != "" {
		fullPrompt = prompt + "\n" + input
	}

	if err := streamChat(baseURL, apiKey, model, fullPrompt, customHeaders); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// customTransport wraps an http.RoundTripper and injects extra headers.
type customTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *customTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

func parseEnvHeaders(envVal string) map[string]string {
	headers := make(map[string]string)
	for _, pair := range strings.Split(envVal, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return headers
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func streamChat(baseURL, apiKey, model, prompt string, customHeaders map[string]string) error {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL

	if len(customHeaders) > 0 {
		config.HTTPClient = &http.Client{
			Transport: &customTransport{
				base:    http.DefaultTransport,
				headers: customHeaders,
			},
		}
	}

	client := openai.NewClientWithConfig(config)

	stream, err := client.CreateChatCompletionStream(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
			Stream: true,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create chat completion stream: %w", err)
	}
	defer stream.Close()

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		if len(response.Choices) > 0 {
			fmt.Print(response.Choices[0].Delta.Content)
		}
	}
	fmt.Println()

	return nil
}
