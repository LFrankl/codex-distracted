package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"codex/llm"
)

// outlineRule maps a symbol kind to a compiled regex.
// The first capture group must be the symbol name.
type outlineRule struct {
	kind string
	re   *regexp.Regexp
}

// perLang holds outline rules indexed by file extension.
var perLang = map[string][]outlineRule{
	".go": {
		{"func", regexp.MustCompile(`^func\s+(?:\(\w[^)]*\)\s+)?(\w+)\s*[(\[]`)},
		{"type", regexp.MustCompile(`^type\s+(\w+)\s+`)},
		{"const", regexp.MustCompile(`^const\s+(\w+)\s`)},
		{"var", regexp.MustCompile(`^var\s+(\w+)\s`)},
	},
	".py": {
		{"class", regexp.MustCompile(`^class\s+(\w+)`)},
		{"func", regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)\s*\(`)},
	},
	".ts": {
		{"class", regexp.MustCompile(`^(?:export\s+)?(?:abstract\s+)?class\s+(\w+)`)},
		{"interface", regexp.MustCompile(`^(?:export\s+)?interface\s+(\w+)`)},
		{"type", regexp.MustCompile(`^(?:export\s+)?type\s+(\w+)\s*=`)},
		{"func", regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)\s*[(<]`)},
		{"func", regexp.MustCompile(`^(?:export\s+)?(?:const|let)\s+(\w+)\s*=\s*(?:async\s*)?\(`)},
	},
	".tsx": nil, // filled below
	".js":  nil,
	".jsx": nil,
	".rs": {
		{"fn", regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+(\w+)\s*[(<]`)},
		{"struct", regexp.MustCompile(`^(?:pub\s+)?struct\s+(\w+)`)},
		{"enum", regexp.MustCompile(`^(?:pub\s+)?enum\s+(\w+)`)},
		{"trait", regexp.MustCompile(`^(?:pub\s+)?trait\s+(\w+)`)},
		{"impl", regexp.MustCompile(`^(?:pub\s+)?impl(?:<[^>]*>)?\s+(?:\w+\s+for\s+)?(\w+)`)},
	},
	".java": {
		{"class", regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|abstract\s+|final\s+)*class\s+(\w+)`)},
		{"interface", regexp.MustCompile(`^\s*(?:public\s+)?interface\s+(\w+)`)},
		{"method", regexp.MustCompile(`^\s*(?:public|private|protected|static|final|abstract|synchronized)[\w\s<>[\],]*\s+(\w+)\s*\(`)},
	},
	".kt": {
		{"class", regexp.MustCompile(`^(?:data\s+|open\s+|abstract\s+)?class\s+(\w+)`)},
		{"object", regexp.MustCompile(`^object\s+(\w+)`)},
		{"fun", regexp.MustCompile(`^(?:suspend\s+)?fun\s+(\w+)\s*[(<]`)},
	},
	".rb": {
		{"class", regexp.MustCompile(`^(?:class|module)\s+(\w+)`)},
		{"def", regexp.MustCompile(`^\s*def\s+(\w+)`)},
	},
	".c":  nil, // filled below
	".cc": nil,
	".cpp": {
		{"func", regexp.MustCompile(`^(?:\w[\w\s*&<>:,]*\s+)?(\w+)\s*\([^;]*$`)},
		{"struct", regexp.MustCompile(`^(?:struct|class|union|enum)\s+(\w+)`)},
	},
	".h": nil,
}

func init() {
	// Share rules across similar extensions
	perLang[".tsx"] = perLang[".ts"]
	perLang[".js"] = perLang[".ts"]
	perLang[".jsx"] = perLang[".ts"]
	perLang[".c"] = perLang[".cpp"]
	perLang[".cc"] = perLang[".cpp"]
	perLang[".h"] = perLang[".cpp"]
}

type outlineSymbol struct {
	Line int
	Kind string
	Name string
}

func (r *ToolRegistry) defFileOutline() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name: "file_outline",
			Description: `List all top-level symbols (functions, classes, types, etc.) in a file with their line numbers.
Much cheaper than read_file for large files — use this to understand structure before diving in.
Supported: Go, Python, TypeScript/JavaScript, Rust, Java, Kotlin, Ruby, C/C++.`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path (relative to working directory or absolute)",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (r *ToolRegistry) fileOutline(argsJSON string) ToolResult {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ToolResult{Content: "invalid args: " + err.Error(), IsError: true}
	}
	if args.Path == "" {
		return ToolResult{Content: "path is required", IsError: true}
	}

	full := args.Path
	if !filepath.IsAbs(full) {
		full = filepath.Join(r.workDir, full)
	}

	data, err := os.ReadFile(full)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("cannot read file: %v", err), IsError: true}
	}

	ext := strings.ToLower(filepath.Ext(full))
	rules, ok := perLang[ext]
	if !ok || rules == nil {
		return ToolResult{Content: fmt.Sprintf("unsupported file type: %s\nSupported: .go .py .ts .tsx .js .jsx .rs .java .kt .rb .c .cc .cpp .h", ext), IsError: true}
	}

	symbols := extractSymbols(string(data), rules)
	if len(symbols) == 0 {
		return ToolResult{Content: fmt.Sprintf("%s — no symbols found", args.Path)}
	}

	// Format as aligned table
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n\n", args.Path)

	// Measure widths
	maxKind, maxName := 4, 4
	for _, s := range symbols {
		if len(s.Kind) > maxKind {
			maxKind = len(s.Kind)
		}
		if len(s.Name) > maxName {
			maxName = len(s.Name)
		}
	}

	header := fmt.Sprintf("%-*s  %-*s  Line", maxKind, "Kind", maxName, "Name")
	sb.WriteString(header)
	sb.WriteByte('\n')
	sb.WriteString(strings.Repeat("─", len(header)))
	sb.WriteByte('\n')

	for _, s := range symbols {
		fmt.Fprintf(&sb, "%-*s  %-*s  %d\n", maxKind, s.Kind, maxName, s.Name, s.Line)
	}

	return ToolResult{Content: sb.String()}
}

func extractSymbols(src string, rules []outlineRule) []outlineSymbol {
	var symbols []outlineSymbol
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		for _, rule := range rules {
			m := rule.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := ""
			if len(m) > 1 {
				name = m[1]
			}
			if name == "" {
				continue
			}
			symbols = append(symbols, outlineSymbol{
				Line: i + 1,
				Kind: rule.kind,
				Name: name,
			})
			break // one symbol per line
		}
	}
	return symbols
}
