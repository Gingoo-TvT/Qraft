package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Protocol identifies the HTTP wire contract used for an LLM request.
type Protocol string

const (
	ProtocolAuto            Protocol = "auto"
	ProtocolAnthropic       Protocol = "anthropic-messages"
	ProtocolGemini          Protocol = "gemini-native"
	ProtocolOpenAIResponses Protocol = "openai-responses"
	ProtocolOpenAIChat      Protocol = "openai-chat"
)

// ResolveProtocol honors an explicit runtime protocol and otherwise infers a
// safe default from the model and provider identities.
func ResolveProtocol(runtime *RuntimeConfig, model string) (Protocol, error) {
	configured := ""
	provider := ""
	if runtime != nil {
		configured = strings.ToLower(strings.TrimSpace(runtime.Protocol))
		provider = strings.ToLower(strings.TrimSpace(runtime.Provider))
	}
	switch configured {
	case "", string(ProtocolAuto):
		return inferProtocol(provider, model), nil
	case string(ProtocolAnthropic), "anthropic", "messages", "claude":
		return ProtocolAnthropic, nil
	case string(ProtocolGemini), "gemini", "google":
		return ProtocolGemini, nil
	case string(ProtocolOpenAIResponses), "responses", "openai":
		return ProtocolOpenAIResponses, nil
	case string(ProtocolOpenAIChat), "chat-completions", "openai-compatible":
		return ProtocolOpenAIChat, nil
	default:
		return "", fmt.Errorf("unsupported LLM protocol %q", configured)
	}
}

func inferProtocol(provider, model string) Protocol {
	model = strings.ToLower(strings.TrimSpace(model))
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch {
	case strings.HasPrefix(model, "gemini-"):
		return ProtocolGemini
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o1"),
		strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		return ProtocolOpenAIResponses
	case strings.HasPrefix(model, "claude-"):
		return ProtocolAnthropic
	case strings.Contains(provider, "gemini"), strings.Contains(provider, "google"):
		return ProtocolGemini
	case strings.Contains(provider, "anthropic"), strings.Contains(provider, "claude"):
		return ProtocolAnthropic
	case strings.Contains(provider, "linkapi"), strings.Contains(provider, "openai"):
		return ProtocolOpenAIChat
	default:
		// Preserve the repository's historical default for custom endpoints.
		return ProtocolAnthropic
	}
}

func (c *Client) completeProtocol(
	ctx context.Context,
	req *Request,
	baseURL, apiKey string,
	protocol Protocol,
) (*Response, error) {
	switch protocol {
	case ProtocolAnthropic:
		return c.completeAnthropic(ctx, req, baseURL, apiKey)
	case ProtocolGemini:
		return c.completeGemini(ctx, req, baseURL, apiKey)
	case ProtocolOpenAIResponses:
		return c.completeOpenAIResponses(ctx, req, baseURL, apiKey)
	case ProtocolOpenAIChat:
		return c.completeOpenAIChat(ctx, req, baseURL, apiKey)
	default:
		return nil, fmt.Errorf("unsupported LLM protocol %q", protocol)
	}
}

func (c *Client) completeAnthropic(ctx context.Context, req *Request, baseURL, apiKey string) (*Response, error) {
	resp, err := c.sendJSON(ctx, normalizeMessagesURL(baseURL), req, func(httpReq *http.Request) {
		c.setHeaders(httpReq, apiKey)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	result, err := c.handleResponse(resp)
	if result != nil {
		result.Protocol = string(ProtocolAnthropic)
	}
	return result, err
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens  int      `json:"maxOutputTokens,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	StopSequences    []string `json:"stopSequences,omitempty"`
	ResponseMIMEType string   `json:"responseMimeType,omitempty"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Contents          []geminiContent        `json:"contents"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
}

type geminiResponse struct {
	ResponseID   string `json:"responseId"`
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (c *Client) completeGemini(ctx context.Context, req *Request, baseURL, apiKey string) (*Response, error) {
	payload := geminiRequest{
		GenerationConfig: geminiGenerationConfig{
			MaxOutputTokens:  req.MaxTokens,
			Temperature:      req.Temperature,
			StopSequences:    req.StopSequences,
			ResponseMIMEType: "application/json",
		},
	}
	if strings.TrimSpace(req.System) != "" {
		payload.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: req.System}}}
	}
	for _, message := range messagesWithoutTerminalPrefill(req.Messages) {
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		payload.Contents = append(payload.Contents, geminiContent{
			Role: role, Parts: []geminiPart{{Text: message.Content}},
		})
	}
	resp, err := c.sendJSON(ctx, normalizeGeminiURL(baseURL, req.Model), payload, func(httpReq *http.Request) {
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", apiKey)
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseProtocolErrorResponse(resp, ProtocolGemini)
	}
	body, truncated, err := readProviderResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading Gemini response: %w", err)
	}
	if truncated {
		return nil, &ResponseDecodeError{Body: body, Truncated: true}
	}
	var decoded geminiResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ResponseDecodeError{Cause: err, Body: body}
	}
	result := &Response{
		ID: decoded.ResponseID, Type: "message", Role: "assistant",
		Protocol: string(ProtocolGemini), Model: decoded.ModelVersion,
	}
	result.ModelObserved = strings.TrimSpace(result.Model) != ""
	if result.Model == "" {
		result.Model = req.Model
	}
	if len(decoded.Candidates) > 0 {
		for _, part := range decoded.Candidates[0].Content.Parts {
			result.Content = append(result.Content, ContentBlock{Type: "text", Text: part.Text})
		}
		result.StopReason = normalizeGeminiFinishReason(decoded.Candidates[0].FinishReason)
	}
	result.Usage = Usage{
		InputTokens:  decoded.UsageMetadata.PromptTokenCount,
		OutputTokens: decoded.UsageMetadata.CandidatesTokenCount,
	}
	return result, nil
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponsesRequest struct {
	Model           string           `json:"model"`
	Instructions    string           `json:"instructions,omitempty"`
	Input           []openAIMessage  `json:"input"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Reasoning       *openAIReasoning `json:"reasoning,omitempty"`
}

type openAIReasoning struct {
	Effort string `json:"effort"`
}

type openAIResponsesResponse struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	OutputText string `json:"output_text"`
	Output     []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (c *Client) completeOpenAIResponses(ctx context.Context, req *Request, baseURL, apiKey string) (*Response, error) {
	payload := openAIResponsesRequest{
		Model: req.Model, Instructions: req.System, MaxOutputTokens: req.MaxTokens,
	}
	if effort := requestReasoningEffort(req); effort != "" {
		payload.Reasoning = &openAIReasoning{Effort: effort}
	}
	for _, message := range messagesWithoutTerminalPrefill(req.Messages) {
		payload.Input = append(payload.Input, openAIMessage{Role: message.Role, Content: message.Content})
	}
	resp, err := c.sendJSON(ctx, normalizeOpenAIURL(baseURL, "responses"), payload, bearerHeaders(apiKey))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseProtocolErrorResponse(resp, ProtocolOpenAIResponses)
	}
	body, truncated, err := readProviderResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading OpenAI Responses response: %w", err)
	}
	if truncated {
		return nil, &ResponseDecodeError{Body: body, Truncated: true}
	}
	var decoded openAIResponsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ResponseDecodeError{Cause: err, Body: body}
	}
	result := &Response{
		ID: decoded.ID, Type: "message", Role: "assistant",
		Protocol: string(ProtocolOpenAIResponses), Model: decoded.Model, StopReason: "end_turn",
	}
	result.ModelObserved = strings.TrimSpace(result.Model) != ""
	if result.Model == "" {
		result.Model = req.Model
	}
	text := decoded.OutputText
	if text == "" {
		for _, item := range decoded.Output {
			for _, content := range item.Content {
				if content.Type == "output_text" || content.Type == "text" {
					text += content.Text
				}
			}
		}
	}
	result.Content = []ContentBlock{{Type: "text", Text: text}}
	if decoded.Status == "incomplete" {
		result.StopReason = decoded.IncompleteDetails.Reason
		if strings.Contains(result.StopReason, "max") {
			result.StopReason = "max_tokens"
		}
	}
	result.Usage = Usage{InputTokens: decoded.Usage.InputTokens, OutputTokens: decoded.Usage.OutputTokens}
	return result, nil
}

type openAIChatRequest struct {
	Model           string          `json:"model"`
	Messages        []openAIMessage `json:"messages"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	Stop            []string        `json:"stop,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *Client) completeOpenAIChat(ctx context.Context, req *Request, baseURL, apiKey string) (*Response, error) {
	payload := openAIChatRequest{
		Model: req.Model, MaxTokens: req.MaxTokens,
		Temperature: req.Temperature, Stop: req.StopSequences,
	}
	payload.ReasoningEffort = requestReasoningEffort(req)
	if strings.TrimSpace(req.System) != "" {
		payload.Messages = append(payload.Messages, openAIMessage{Role: "system", Content: req.System})
	}
	for _, message := range messagesWithoutTerminalPrefill(req.Messages) {
		payload.Messages = append(payload.Messages, openAIMessage{Role: message.Role, Content: message.Content})
	}
	resp, err := c.sendJSON(ctx, normalizeOpenAIURL(baseURL, "chat/completions"), payload, bearerHeaders(apiKey))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseProtocolErrorResponse(resp, ProtocolOpenAIChat)
	}
	body, truncated, err := readProviderResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading OpenAI Chat response: %w", err)
	}
	if truncated {
		return nil, &ResponseDecodeError{Body: body, Truncated: true}
	}
	var decoded openAIChatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, &ResponseDecodeError{Cause: err, Body: body}
	}
	result := &Response{
		ID: decoded.ID, Type: "message", Role: "assistant",
		Protocol: string(ProtocolOpenAIChat), Model: decoded.Model,
	}
	result.ModelObserved = strings.TrimSpace(result.Model) != ""
	if result.Model == "" {
		result.Model = req.Model
	}
	if len(decoded.Choices) > 0 {
		text, err := decodeChatContent(decoded.Choices[0].Message.Content)
		if err != nil {
			return nil, &ResponseDecodeError{Cause: err, Body: body}
		}
		result.Content = []ContentBlock{{Type: "text", Text: text}}
		result.StopReason = decoded.Choices[0].FinishReason
		if result.StopReason == "length" {
			result.StopReason = "max_tokens"
		}
	}
	result.Usage = Usage{
		InputTokens:  decoded.Usage.PromptTokens,
		OutputTokens: decoded.Usage.CompletionTokens,
	}
	return result, nil
}

func requestReasoningEffort(req *Request) string {
	if req == nil || req.Runtime == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(req.Runtime.ReasoningEffort))
}

// messagesWithoutTerminalPrefill keeps few-shot assistant messages but drops
// the final "{" prefill that Gemini and OpenAI protocols reject.
func messagesWithoutTerminalPrefill(messages []Message) []Message {
	result := append([]Message(nil), messages...)
	if len(result) == 0 {
		return result
	}
	last := result[len(result)-1]
	if last.Role == "assistant" && strings.TrimSpace(last.Content) == "{" {
		return result[:len(result)-1]
	}
	return result
}

func normalizeOpenAIURL(baseURL, endpoint string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	wanted := "/" + strings.TrimLeft(endpoint, "/")
	if strings.HasSuffix(baseURL, wanted) {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + wanted
	}
	return baseURL + "/v1" + wanted
}

func normalizeGeminiURL(baseURL, model string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if strings.Contains(baseURL, ":generateContent") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
	}
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	model = url.PathEscape(model)
	if strings.HasSuffix(baseURL, "/v1beta/models") {
		return baseURL + "/" + model + ":generateContent"
	}
	if strings.HasSuffix(baseURL, "/v1beta") {
		return baseURL + "/models/" + model + ":generateContent"
	}
	return baseURL + "/v1beta/models/" + model + ":generateContent"
}

func bearerHeaders(apiKey string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func (c *Client) sendJSON(
	ctx context.Context,
	endpoint string,
	payload interface{},
	headers func(*http.Request),
) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling LLM request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating LLM request: %w", err)
	}
	headers(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing LLM request: %w", err)
	}
	return resp, nil
}

func (c *Client) parseProtocolErrorResponse(resp *http.Response, protocol Protocol) error {
	body, truncated, err := readProviderResponse(resp.Body)
	if err != nil {
		return &APIError{
			StatusCode: resp.StatusCode, Type: "read_error",
			Message: err.Error(), Protocol: string(protocol),
		}
	}
	apiErr := APIError{
		StatusCode: resp.StatusCode, Type: "provider_error",
		Message: http.StatusText(resp.StatusCode), Protocol: string(protocol),
		responseEvidence: body, responseEvidenceTruncated: truncated,
	}
	if !truncated {
		var envelope struct {
			Error struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil {
			if envelope.Error.Type != "" {
				apiErr.Type = envelope.Error.Type
			} else if envelope.Error.Status != "" {
				apiErr.Type = envelope.Error.Status
			}
			if envelope.Error.Message != "" {
				apiErr.Message = envelope.Error.Message
			}
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		rateLimitErr := &RateLimitError{APIError: apiErr}
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil {
				rateLimitErr.RetryAfterSeconds = seconds
			}
		}
		return rateLimitErr
	}
	return &apiErr
}

func decodeChatContent(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.Text)
	}
	return builder.String(), nil
}

func normalizeGeminiFinishReason(reason string) string {
	switch strings.ToUpper(reason) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP":
		return "end_turn"
	default:
		return strings.ToLower(reason)
	}
}
