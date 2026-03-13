package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Provider struct {
	Name       string `yaml:"name"`
	BaseURL    string `yaml:"base_url"`
	APIKey     string `yaml:"api_key"`
	Model      string `yaml:"model"`
	EmbedModel string `yaml:"embed_model,omitempty"` // model for /embeddings API; empty = RAG disabled
}

type Config struct {
	CurrentProvider string              `yaml:"current_provider"`
	Providers       map[string]Provider `yaml:"providers"`
	MaxSteps        int                 `yaml:"max_steps"`
	WorkDir         string              `yaml:"work_dir"`
}

var builtinProviders = map[string]Provider{
	"deepseek": {
		Name:    "deepseek",
		BaseURL: "https://api.deepseek.com/v1",
		Model:   "deepseek-chat",
	},
	"qwen": {
		Name:    "qwen",
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Model:   "qwen-max",
	},
	"zhipu": {
		Name:    "zhipu",
		BaseURL: "https://open.bigmodel.cn/api/paas/v4",
		Model:   "glm-4",
	},
	"moonshot": {
		Name:    "moonshot",
		BaseURL: "https://api.moonshot.cn/v1",
		Model:   "moonshot-v1-8k",
	},
}

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.yaml")
}

func Load() (*Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(ConfigPath())
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Never use a persisted WorkDir — always resolve at runtime from os.Getwd().
	// Old config files may contain a stale path from a previous session.
	cfg.WorkDir = ""

	// Merge builtins (don't overwrite user-defined)
	for name, p := range builtinProviders {
		if _, exists := cfg.Providers[name]; !exists {
			cfg.Providers[name] = p
		}
	}

	return cfg, nil
}

func (c *Config) Save() error {
	if err := os.MkdirAll(ConfigDir(), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0600)
}

func (c *Config) GetCurrentProvider() (*Provider, error) {
	name := c.CurrentProvider
	if name == "" {
		return nil, fmt.Errorf("no provider configured, run: codex config set-provider <name>")
	}
	p, ok := c.Providers[name]
	if !ok {
		return nil, fmt.Errorf("provider %q not found", name)
	}
	if p.APIKey == "" {
		return nil, fmt.Errorf("API key not set for provider %q, run: codex config set-key %s <your-key>", name, name)
	}
	return &p, nil
}

func defaultConfig() *Config {
	cfg := &Config{
		MaxSteps:  10,
		Providers: make(map[string]Provider),
	}
	for name, p := range builtinProviders {
		cfg.Providers[name] = p
	}
	return cfg
}

func BuiltinProviders() map[string]Provider {
	return builtinProviders
}
