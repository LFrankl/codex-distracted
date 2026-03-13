package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// --- Request/Response types (OpenAI compatible) ---

type Message struct {
	Role       string      `json:"role"`
	Content    any         `json:"content"` // string or []ContentPart
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	Name       string      `json:"name,omitempty"`
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
	Index    int          `json:"index"` // used in streaming deltas
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Stream      bool      `json:"stream"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
	Error   *APIError `json:"error,omitempty"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error [%v]: %s", e.Code, e.Message)
}

// --- Client ---

type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// StreamEvent is emitted during streaming
type StreamEvent struct {
	Content   string     // text delta
	ToolCalls []ToolCall // accumulated tool calls when done
	Usage     *Usage     // token usage, present in the Done event (if provider reports it)
	Done      bool
	Error     error
}

// Chat sends a request and streams response events.
// onEvent is called for each token; final event has Done=true with full tool calls.
func (c *Client) Chat(ctx context.Context, msgs []Message, tools []Tool, onEvent func(StreamEvent)) error {
	req := ChatRequest{
		Model:     c.model,
		Messages:  msgs,
		Tools:     tools,
		Stream:    true,
		MaxTokens: 4096,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		var apiResp ChatResponse
		if json.Unmarshal(data, &apiResp) == nil && apiResp.Error != nil {
			return apiResp.Error
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	return c.parseStream(resp.Body, onEvent)
}

func (c *Client) parseStream(r io.Reader, onEvent func(StreamEvent)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Accumulate tool calls across chunks
	toolCallMap := map[int]*ToolCall{}
	var lastUsage *Usage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			var toolCalls []ToolCall
			for i := 0; i < len(toolCallMap); i++ {
				if tc, ok := toolCallMap[i]; ok {
					toolCalls = append(toolCalls, *tc)
				}
			}
			onEvent(StreamEvent{Done: true, ToolCalls: toolCalls, Usage: lastUsage})
			return nil
		}

		var chunk ChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		// Some providers send usage in a separate chunk before [DONE]
		if chunk.Usage.TotalTokens > 0 {
			u := chunk.Usage
			lastUsage = &u
		}
		if chunk.Error != nil {
			return chunk.Error
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Text content
		if content, ok := delta.Content.(string); ok && content != "" {
			onEvent(StreamEvent{Content: content})
		}

		// Tool call deltas
		for _, tc := range delta.ToolCalls {
			existing, ok := toolCallMap[tc.Index]
			if !ok {
				toolCallMap[tc.Index] = &ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: FunctionCall{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			} else {
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Function.Name += tc.Function.Name
				}
				existing.Function.Arguments += tc.Function.Arguments
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read: %w", err)
	}
	return nil
}

// NonStreamChat for providers that don't support streaming well
func (c *Client) NonStreamChat(ctx context.Context, msgs []Message, tools []Tool) (*Message, error) {
	req := ChatRequest{
		Model:     c.model,
		Messages:  msgs,
		Tools:     tools,
		Stream:    false,
		MaxTokens: 4096,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var apiResp ChatResponse
	if err := json.Unmarshal(data, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if apiResp.Error != nil {
		return nil, apiResp.Error
	}
	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	msg := apiResp.Choices[0].Message
	return &msg, nil
}
