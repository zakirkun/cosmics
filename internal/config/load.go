package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cosmics/cosmics/internal/domain"
)

// Load reads, expands, defaults, and validates a configuration file.
func Load(ctx context.Context, path string) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	expanded := os.Expand(string(raw), os.Getenv)

	cfg := Default()
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	applyDefaults(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

// LoadFromEnv builds a configuration from environment variables alone, so the
// tool is usable with no config file present.
func LoadFromEnv(ctx context.Context) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, fmt.Errorf("load config from env: %w", err)
	}

	cfg := Default()

	baseURL := os.Getenv("NINEROUTER_URL")
	if baseURL == "" {
		baseURL = "http://localhost:20128/v1"
	}
	cfg.Providers["9router"] = Provider{
		Kind:       ProviderOpenAICompatible,
		BaseURL:    baseURL,
		APIKeyEnv:  "NINEROUTER_KEY",
		TimeoutS:   120,
		MaxRetries: 2,
	}

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		cfg.Providers["anthropic"] = Provider{
			Kind:             ProviderAnthropic,
			BaseURL:          "https://api.anthropic.com/v1",
			APIKeyEnv:        "ANTHROPIC_API_KEY",
			AnthropicVersion: "2023-06-01",
			TimeoutS:         180,
			MaxRetries:       2,
		}
	}

	applyDefaults(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate environment configuration: %w", err)
	}
	return cfg, nil
}

// Default returns the baseline configuration, including a two-entry fallback
// chain for every pipeline stage and for compaction.
func Default() Config {
	return Config{
		App: AppConfig{
			Operator: os.Getenv("USER"),
			DataDir:  defaultDataDir(),
			LogLevel: "info",
			Theme:    "dark",
		},
		Providers: defaultProviders(),
		ModelProfiles: map[string]ModelProfile{
			"default": defaultModelProfile(),
		},
		Tools: ToolsConfig{
			DefaultTimeoutS:        300,
			MaxOutputBytes:         262144,
			WorkspaceRetentionDays: 0,
		},
		Exploit: ExploitConfig{
			AutoApproveIntrusive: false,
			ApprovalTimeoutS:     300,
			MaxAttemptsPerPath:   3,
		},
		Memory: MemoryConfig{
			RetrievalMode:       RetrievalKeyword,
			TopK:                8,
			TokenBudget:         6000,
			CompactionThreshold: 0.75,
		},
		OSINT: OSINTConfig{
			EnabledSources: []string{"crtsh", "dns", "rdap", "wayback", "github"},
			APIKeysEnv: map[string]string{
				"shodan":     "SHODAN_API_KEY",
				"censys":     "CENSYS_API_ID",
				"virustotal": "VT_API_KEY",
			},
		},
	}
}

// defaultProviders returns the shipped provider defaults.
func defaultProviders() map[string]Provider {
	return map[string]Provider{
		"9router": {
			Kind:       ProviderOpenAICompatible,
			BaseURL:    "http://localhost:20128/v1",
			APIKeyEnv:  "NINEROUTER_KEY",
			TimeoutS:   120,
			MaxRetries: 2,
		},
	}
}

// Both identifiers were confirmed present on the live gateway. The combo
// resolves to the flash model, so model_requested and model_served diverge.
const (
	ModelCombo = "astral-projection"
	ModelFlash = "cutad/deepseek-v4.1-flash"
)

func defaultModelProfile() ModelProfile {
	combo := ModelEntry{Provider: "9router", Model: ModelCombo, MaxTokens: 4096}
	direct := ModelEntry{Provider: "9router", Model: ModelFlash, MaxTokens: 4096}

	profile := make(ModelProfile, len(domain.AllStages)+1)
	for _, stage := range domain.AllStages {
		profile[stage] = []ModelEntry{combo, direct}
	}
	profile[CompactStageKey] = []ModelEntry{direct, combo}
	return profile
}

// defaultDataDir resolves the conventional data directory.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cosmics"
	}
	return filepath.Join(home, ".cosmics")
}

// applyDefaults fills fields left zero by an explicit but partial file.
func applyDefaults(cfg *Config) {
	if cfg.App.LogLevel == "" {
		cfg.App.LogLevel = "info"
	}
	if cfg.App.DataDir == "" {
		cfg.App.DataDir = defaultDataDir()
	}
	if cfg.App.Theme == "" {
		cfg.App.Theme = "dark"
	}
	if len(cfg.Providers) == 0 {
		cfg.Providers = defaultProviders()
	}
	if len(cfg.ModelProfiles) == 0 {
		cfg.ModelProfiles = map[string]ModelProfile{"default": defaultModelProfile()}
	}
	if cfg.Tools.DefaultTimeoutS == 0 {
		cfg.Tools.DefaultTimeoutS = 300
	}
	if cfg.Tools.MaxOutputBytes == 0 {
		cfg.Tools.MaxOutputBytes = 262144
	}
	if cfg.Exploit.ApprovalTimeoutS == 0 {
		cfg.Exploit.ApprovalTimeoutS = 300
	}
	if cfg.Exploit.MaxAttemptsPerPath == 0 {
		cfg.Exploit.MaxAttemptsPerPath = 3
	}
	if cfg.Memory.RetrievalMode == "" {
		cfg.Memory.RetrievalMode = RetrievalKeyword
	}
	if cfg.Memory.TopK == 0 {
		cfg.Memory.TopK = 8
	}
	if cfg.Memory.TokenBudget == 0 {
		cfg.Memory.TokenBudget = 6000
	}
	if cfg.Memory.CompactionThreshold == 0 {
		cfg.Memory.CompactionThreshold = 0.75
	}
}

// DataDir expands a leading tilde and returns an absolute path without creating
// anything.
func (c Config) DataDir() (string, error) {
	dir := strings.TrimSpace(c.App.DataDir)
	if dir == "" {
		dir = defaultDataDir()
	}

	if dir == "~" || strings.HasPrefix(dir, "~/") || strings.HasPrefix(dir, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, strings.TrimLeft(dir[1:], `/\`))
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	return abs, nil
}

// ErrUnknownYAMLKey is reserved for strict decoding; callers may ignore it.
var ErrUnknownYAMLKey = errors.New("config: unknown key")
