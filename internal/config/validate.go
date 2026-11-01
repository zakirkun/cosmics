package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
)

// Sentinel errors raised by validation and profile resolution.
var (
	ErrInvalidConfig        = errors.New("config: invalid configuration")
	ErrUnknownProfile       = errors.New("config: unknown model profile")
	ErrNoModelForStage      = errors.New("config: no model configured for stage")
	ErrEmbeddingUnavailable = errors.New("config: retrieval mode requires embeddings")
)

// maxTimeoutSeconds bounds a single provider or tool operation.
const maxTimeoutSeconds = 3600

// Validate accumulates every problem and reports them together. A caller sees
// the whole list instead of fixing one field per run.
func (c Config) Validate() error {
	var problems []error

	problems = append(problems, c.validateApp()...)
	problems = append(problems, c.validateProviders()...)
	problems = append(problems, c.validateProfiles()...)
	problems = append(problems, c.validateTools()...)
	problems = append(problems, c.validateExploit()...)
	problems = append(problems, c.validateMemory()...)

	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, errors.Join(problems...))
	}
	return nil
}

// validateApp checks operator-facing settings.
func (c Config) validateApp() []error {
	var problems []error

	switch c.App.LogLevel {
	case "debug", "info", "warn", "error":
	case "":
		problems = append(problems, errors.New("app.log_level must be one of debug, info, warn, error"))
	default:
		problems = append(problems, fmt.Errorf("app.log_level %q must be one of debug, info, warn, error", c.App.LogLevel))
	}

	if strings.TrimSpace(c.App.DataDir) == "" {
		problems = append(problems, errors.New("app.data_dir must not be empty"))
	}
	return problems
}

// validateProviders checks every declared provider.
func (c Config) validateProviders() []error {
	var problems []error

	if len(c.Providers) == 0 {
		return []error{errors.New("providers must declare at least one provider")}
	}

	for name, p := range c.Providers {
		switch p.Kind {
		case ProviderOpenAICompatible, ProviderAnthropic:
		default:
			problems = append(problems, fmt.Errorf("providers.%s.kind %q must be %q or %q",
				name, p.Kind, ProviderOpenAICompatible, ProviderAnthropic))
		}

		parsed, err := url.Parse(p.BaseURL)
		switch {
		case err != nil:
			problems = append(problems, fmt.Errorf("providers.%s.base_url is not a URL: %w", name, err))
		case !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https"):
			problems = append(problems, fmt.Errorf("providers.%s.base_url %q must be an absolute http(s) URL", name, p.BaseURL))
		}

		if strings.TrimSpace(p.APIKeyEnv) == "" {
			problems = append(problems, fmt.Errorf("providers.%s.api_key_env must name the environment variable holding the key", name))
		}

		if p.TimeoutS <= 0 || p.TimeoutS > maxTimeoutSeconds {
			problems = append(problems, fmt.Errorf("providers.%s.timeout_s %d must be between 1 and %d", name, p.TimeoutS, maxTimeoutSeconds))
		}
		if p.MaxRetries < 0 {
			problems = append(problems, fmt.Errorf("providers.%s.max_retries %d must not be negative", name, p.MaxRetries))
		}
	}
	return problems
}

// validateProfiles checks every model chain against the declared providers.
func (c Config) validateProfiles() []error {
	var problems []error

	if len(c.ModelProfiles) == 0 {
		return []error{errors.New("model_profiles must declare at least one profile")}
	}

	required := append(slices.Clone(domain.AllStages), CompactStageKey)

	for profileName, profile := range c.ModelProfiles {
		for _, stage := range required {
			chain := profile[stage]
			if len(chain) == 0 {
				problems = append(problems, fmt.Errorf("model_profiles.%s.%s must declare at least one model", profileName, stage))
				continue
			}
			for i, entry := range chain {
				where := fmt.Sprintf("model_profiles.%s.%s[%d]", profileName, stage, i)
				if strings.TrimSpace(entry.Provider) == "" {
					problems = append(problems, fmt.Errorf("%s.provider must not be empty", where))
				} else if _, ok := c.Providers[entry.Provider]; !ok {
					problems = append(problems, fmt.Errorf("%s.provider %q is not declared in providers", where, entry.Provider))
				}
				if strings.TrimSpace(entry.Model) == "" {
					problems = append(problems, fmt.Errorf("%s.model must not be empty", where))
				}
				if entry.MaxTokens <= 0 {
					problems = append(problems, fmt.Errorf("%s.max_tokens %d must be positive", where, entry.MaxTokens))
				}
			}
		}
	}
	return problems
}

// validateTools bounds tool execution settings.
func (c Config) validateTools() []error {
	var problems []error

	if c.Tools.DefaultTimeoutS <= 0 {
		problems = append(problems, fmt.Errorf("tools.default_timeout_s %d must be positive", c.Tools.DefaultTimeoutS))
	}
	if c.Tools.MaxOutputBytes <= 0 {
		problems = append(problems, fmt.Errorf("tools.max_output_bytes %d must be positive", c.Tools.MaxOutputBytes))
	}
	if c.Tools.WorkspaceRetentionDays < 0 {
		problems = append(problems, fmt.Errorf("tools.workspace_retention_days %d must not be negative", c.Tools.WorkspaceRetentionDays))
	}
	return problems
}

// validateExploit enforces the approval-gate settings.
func (c Config) validateExploit() []error {
	var problems []error

	if c.Exploit.ApprovalTimeoutS <= 0 {
		problems = append(problems, fmt.Errorf("exploit.approval_timeout_s %d must be positive", c.Exploit.ApprovalTimeoutS))
	}
	if c.Exploit.MaxAttemptsPerPath <= 0 {
		problems = append(problems, fmt.Errorf("exploit.max_attempts_per_path %d must be positive", c.Exploit.MaxAttemptsPerPath))
	}
	return problems
}

// validateMemory checks retrieval and compaction bounds.
func (c Config) validateMemory() []error {
	var problems []error

	switch c.Memory.RetrievalMode {
	case RetrievalKeyword:
	case RetrievalVector, RetrievalHybrid:
		if !c.Memory.Embeddings.Enabled || c.Memory.Embeddings.Dimensions <= 0 {
			problems = append(problems, fmt.Errorf(
				"memory.retrieval_mode %q: %w", c.Memory.RetrievalMode, ErrEmbeddingUnavailable))
		}
	case "":
		problems = append(problems, errors.New("memory.retrieval_mode must be one of keyword, vector, hybrid"))
	default:
		problems = append(problems, fmt.Errorf("memory.retrieval_mode %q must be one of keyword, vector, hybrid", c.Memory.RetrievalMode))
	}

	if c.Memory.TopK <= 0 {
		problems = append(problems, fmt.Errorf("memory.top_k %d must be positive", c.Memory.TopK))
	}
	if c.Memory.TokenBudget <= 0 {
		problems = append(problems, fmt.Errorf("memory.token_budget %d must be positive", c.Memory.TokenBudget))
	}
	if c.Memory.CompactionThreshold <= 0 || c.Memory.CompactionThreshold >= 1 {
		problems = append(problems, fmt.Errorf("memory.compaction_threshold %v must be between 0 and 1 exclusive", c.Memory.CompactionThreshold))
	}
	return problems
}

// ResolveModelChain returns the ordered fallback chain for a profile and stage.
func (c Config) ResolveModelChain(profile string, stage domain.StageKind) ([]ModelEntry, error) {
	resolved, ok := c.ModelProfiles[profile]
	if !ok {
		return nil, fmt.Errorf("profile %q: %w", profile, ErrUnknownProfile)
	}
	chain, ok := resolved[stage]
	if !ok || len(chain) == 0 {
		return nil, fmt.Errorf("profile %s stage %s: %w", profile, stage, ErrNoModelForStage)
	}
	return slices.Clone(chain), nil
}
