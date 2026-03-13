package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"codex/llm"
)

const (
	fetchMaxBytes   = 200 * 1024 // 200 KB cap
	fetchTimeoutSec = 15
)

var (
	reNoisy = regexp.MustCompile(`(?si)<(script|style|head|nav|footer|aside)[^>]*>.*?</(script|style|head|nav|footer|aside)>`)
	reTags  = regexp.MustCompile(`<[^>]+>`)
	reSpTab = regexp.MustCompile(`[ \t]+`)
)

func (r *ToolRegistry) defWebFetch() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "web_fetch",
			Description: `Fetch a URL and return its content as plain text.
Strips HTML tags; returns raw content for non-HTML responses (JSON, plain text).
Useful for reading documentation, GitHub issues, API specs, or any web page.
Capped at 200 KB to avoid token overload.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to fetch (http or https)",
					},
					"max_lines": map[string]any{
						"type":        "integer",
						"description": "Truncate output to this many lines (optional, default 300)",
					},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (r *ToolRegistry) webFetch(argsJSON string) ToolResult {
	var args struct {
		URL      string `json:"url"`
		MaxLines int    `json:"max_lines"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.URL == "" {
		return ToolResult{Content: "url is required", IsError: true}
	}
	if args.MaxLines <= 0 {
		args.MaxLines = 300
	}

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeoutSec*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("bad URL: %v", err), IsError: true}
	}
	req.Header.Set("User-Agent", "distracted-codex/1.0 (fetch)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("fetch failed: %v", err), IsError: true}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBytes))
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("read body failed: %v", err), IsError: true}
	}

	ct := resp.Header.Get("Content-Type")
	var text string
	if strings.Contains(ct, "html") || strings.HasPrefix(strings.TrimSpace(string(body)), "<") {
		text = stripHTML(string(body))
	} else {
		text = string(body)
	}

	// Truncate to max_lines
	lines := strings.Split(text, "\n")
	truncated := false
	if len(lines) > args.MaxLines {
		lines = lines[:args.MaxLines]
		truncated = true
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "URL: %s  [HTTP %d]\n\n", args.URL, resp.StatusCode)
	sb.WriteString(strings.Join(lines, "\n"))
	if truncated {
		fmt.Fprintf(&sb, "\n\n[truncated — %d lines shown of %d total]", args.MaxLines, len(strings.Split(text, "\n")))
	}
	return ToolResult{Content: sb.String()}
}

// stripHTML removes HTML markup and returns plain text.
func stripHTML(h string) string {
	h = reNoisy.ReplaceAllString(h, "")
	h = reTags.ReplaceAllString(h, " ")
	h = reSpTab.ReplaceAllString(h, " ")

	var out []string
	for _, line := range strings.Split(h, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}
