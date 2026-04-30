package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
	promptJSON := flag.Bool("prompt-json", false, "treat prompt content as raw JSON messages array for the chat completion API")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintln(flag.CommandLine.Output())
		fmt.Fprintln(flag.CommandLine.Output(), "Examples:")
		fmt.Fprintln(flag.CommandLine.Output(), "  llm_prompt_cli --prompt \"翻译为中文\" --input \"Hello world\"")
		fmt.Fprintln(flag.CommandLine.Output(), "  llm_prompt_cli --prompt-file prompt.txt --input-file data.txt")
		fmt.Fprintln(flag.CommandLine.Output(), "  cat file.txt | llm_prompt_cli --prompt \"总结以下内容\" --stdin")
		fmt.Fprintln(flag.CommandLine.Output(), "  llm_prompt_cli --prompt \"explain this\" --config /path/to/.env")
		fmt.Fprintln(flag.CommandLine.Output(), "  llm_prompt_cli --prompt-json --prompt '[{\"role\":\"system\",\"content\":\"翻译为中文\"},{\"role\":\"user\",\"content\":\"Hello\"}]'")
		fmt.Fprintln(flag.CommandLine.Output(), "  llm_prompt_cli --prompt-json --prompt-file messages.json")
	}
	flag.Parse()

	// no flags provided: show help like --help
	if *promptText == "" && *promptFile == "" && *inputFile == "" && *inputText == "" && !*stdin && *configPath == "" {
		flag.Usage()
		os.Exit(0)
	}

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
		flag.Usage()
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

	params := chatParams{maxTokens: 8192}
	if v := os.Getenv("maxTokens"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "Warning: invalid maxTokens=%q, using default %d\n", v, params.maxTokens)
		} else {
			params.maxTokens = n
		}
	}
	params.temperature = parseFloatEnv("temperature")
	params.topP = parseFloatEnv("topP")
	params.presencePenalty = parseFloatEnv("presencePenalty")
	params.frequencyPenalty = parseFloatEnv("frequencyPenalty")
	if v := os.Getenv("timeout"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "Warning: invalid timeout=%q, ignored\n", v)
		} else {
			params.timeoutSec = n
		}
	}
	params.showThinking = parseBoolEnv("showThinking")
	params.verifyTLS = parseBoolEnvDefault("verifyTLS", false)

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

	// build messages
	var messages []openai.ChatCompletionMessage
	if *promptJSON {
		if err := json.Unmarshal([]byte(prompt), &messages); err != nil {
			fmt.Fprintf(os.Stderr, "Error: --prompt-json requires valid JSON messages array: %v\n", err)
			os.Exit(1)
		}
		if len(messages) == 0 {
			fmt.Fprintln(os.Stderr, "Error: --prompt-json messages array is empty")
			os.Exit(1)
		}
	} else {
		messages = []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		}
	}
	if input != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: input,
		})
	}

	if err := streamChat(baseURL, apiKey, model, messages, customHeaders, params); err != nil {
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

type chatParams struct {
	maxTokens        int
	temperature      *float32
	topP             *float32
	presencePenalty  *float32
	frequencyPenalty *float32
	timeoutSec       int
	showThinking     bool
	verifyTLS        bool
}

func parseFloatEnv(key string) *float32 {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid %s=%q, ignored\n", key, v)
		return nil
	}
	out := float32(f)
	return &out
}

func parseBoolEnv(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func parseBoolEnvDefault(key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	fmt.Fprintf(os.Stderr, "Warning: invalid %s=%q, using default %v\n", key, v, def)
	return def
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

func streamChat(baseURL, apiKey, model string, messages []openai.ChatCompletionMessage, customHeaders map[string]string, params chatParams) error {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = baseURL

	var transport http.RoundTripper = http.DefaultTransport
	if !params.verifyTLS {
		t := http.DefaultTransport.(*http.Transport).Clone()
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.InsecureSkipVerify = true
		transport = t
	}
	if len(customHeaders) > 0 {
		transport = &customTransport{base: transport, headers: customHeaders}
	}
	if !params.verifyTLS || len(customHeaders) > 0 {
		config.HTTPClient = &http.Client{Transport: transport}
	}

	client := openai.NewClientWithConfig(config)

	req := openai.ChatCompletionRequest{
		Model:     model,
		Messages:  messages,
		Stream:    true,
		MaxTokens: params.maxTokens,
	}
	if params.temperature != nil {
		req.Temperature = *params.temperature
	}
	if params.topP != nil {
		req.TopP = *params.topP
	}
	if params.presencePenalty != nil {
		req.PresencePenalty = *params.presencePenalty
	}
	if params.frequencyPenalty != nil {
		req.FrequencyPenalty = *params.frequencyPenalty
	}

	ctx := context.Background()
	if params.timeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(params.timeoutSec)*time.Second)
		defer cancel()
	}

	stream, err := client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to create chat completion stream: %w", err)
	}
	defer stream.Close()

	contentSeen := false
	thinkingOpen := false
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		if len(response.Choices) == 0 {
			continue
		}
		delta := response.Choices[0].Delta

		if params.showThinking && delta.ReasoningContent != "" {
			if !thinkingOpen {
				fmt.Fprint(os.Stderr, "<think>")
				thinkingOpen = true
			}
			fmt.Fprint(os.Stderr, delta.ReasoningContent)
		}
		if delta.Content != "" {
			if thinkingOpen {
				fmt.Fprintln(os.Stderr, "</think>")
				thinkingOpen = false
			}
			fmt.Print(delta.Content)
			contentSeen = true
		}
	}
	if thinkingOpen {
		fmt.Fprintln(os.Stderr, "</think>")
	}
	if contentSeen {
		fmt.Println()
	}

	return nil
}
