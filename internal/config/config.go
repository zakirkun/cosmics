// Package config loads, validates, and redacts the Cosmics configuration.
//
// Secrets are never stored in Config: only the NAME of the environment variable
// that holds each secret is kept, and Redact reads the value at call time.
package config

import (
	"github.com/cosmics/cosmics/internal/domain"
)

// Config is the fully resolved application configuration.
type Config struct {
	App           AppConfig               `yaml:"app"`
	Providers     map[string]Provider     `yaml:"providers"`
	ModelProfiles map[string]ModelProfile `yaml:"model_profiles"`
	Tools         ToolsConfig             `yaml:"tools"`
	Exploit       ExploitConfig           `yaml:"exploit"`
	Memory        MemoryConfig            `yaml:"memory"`
	OSINT         OSINTConfig             `yaml:"osint"`
}

// AppConfig holds operator-facing settings.
type AppConfig struct {
	Operator string `yaml:"operator"`
	DataDir  string `yaml:"data_dir"`
	LogLevel string `yaml:"log_level"`
	Theme    string `yaml:"theme"`
}

// Provider describes one LLM backend.
type Provider struct {
	Kind             string `yaml:"kind"`
	BaseURL          string `yaml:"base_url"`
	APIKeyEnv        string `yaml:"api_key_env"`
	AnthropicVersion string `yaml:"anthropic_version"`
	TimeoutS         int    `yaml:"timeout_s"`
	MaxRetries       int    `yaml:"max_retries"`
}

// Provider kinds.
const (
	ProviderOpenAICompatible = "openai_compatible"
	ProviderAnthropic        = "anthropic"
)

// ModelProfile maps each pipeline stage to an ordered fallback chain.
type ModelProfile map[domain.StageKind][]ModelEntry

// CompactStageKey is the profile key used for context compaction, which is not a
// pipeline stage but still needs a model.
const CompactStageKey domain.StageKind = "compact"

// ModelEntry is one link in a stage's fallback chain.
type ModelEntry struct {
	Provider  string           `yaml:"provider"`
	Model     string           `yaml:"model"`
	MaxTokens int              `yaml:"max_tokens"`
	Reasoning *ReasoningConfig `yaml:"reasoning"`
}

// ReasoningConfig requests extended reasoning when the provider supports it.
type ReasoningConfig struct {
	Effort string `yaml:"effort"`
}

// ToolsConfig bounds tool execution.
type ToolsConfig struct {
	DefaultTimeoutS        int `yaml:"default_timeout_s"`
	MaxOutputBytes         int `yaml:"max_output_bytes"`
	WorkspaceRetentionDays int `yaml:"workspace_retention_days"`
}

// ExploitConfig gates destructive actions.
type ExploitConfig struct {
	AutoApproveIntrusive bool `yaml:"auto_approve_intrusive"`
	ApprovalTimeoutS     int  `yaml:"approval_timeout_s"`
	MaxAttemptsPerPath   int  `yaml:"max_attempts_per_path"`
}

// MemoryConfig controls retrieval and compaction.
type MemoryConfig struct {
	RetrievalMode       string           `yaml:"retrieval_mode"`
	TopK                int              `yaml:"top_k"`
	TokenBudget         int              `yaml:"token_budget"`
	CompactionThreshold float64          `yaml:"compaction_threshold"`
	Embeddings          EmbeddingsConfig `yaml:"embeddings"`
}

// Retrieval modes.
const (
	RetrievalKeyword = "keyword"
	RetrievalVector  = "vector"
	RetrievalHybrid  = "hybrid"
)

// EmbeddingsConfig configures optional vector retrieval.
type EmbeddingsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider"`
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
}

// OSINTConfig selects external intelligence sources.
type OSINTConfig struct {
	EnabledSources []string          `yaml:"enabled_sources"`
	APIKeysEnv     map[string]string `yaml:"api_keys_env"`
}
