// Package doctor runs environment diagnostics and reports them in a form a
// human can act on. It performs real checks and never reports success for a
// component it could not actually verify.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cosmics/cosmics/internal/config"
)

// ErrUnhealthy is returned when at least one required check failed.
var ErrUnhealthy = errors.New("doctor: environment is not healthy")

// Status is the outcome of one check.
type Status string

// Check outcomes.
const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Check is one diagnostic result.
type Check struct {
	Name   string
	Status Status
	Detail string
	Hints  []string
}

// Report is the ordered set of checks plus a summary.
type Report struct {
	Checks []Check
}

// Healthy reports whether every check passed or was skipped.
func (r Report) Healthy() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return false
		}
	}
	return true
}

// Render formats the report for a terminal.
func (r Report) Render() string {
	var b strings.Builder

	for _, c := range r.Checks {
		fmt.Fprintf(&b, "%s  %s — %s\n", glyph(c.Status), c.Name, c.Detail)
		for _, hint := range c.Hints {
			fmt.Fprintf(&b, "    -> %s\n", hint)
		}
	}

	failures, warnings := r.counts()
	if failures == 0 && warnings == 0 {
		b.WriteString("\nall checks passed\n")
	} else {
		fmt.Fprintf(&b, "\n%d failure(s), %d warning(s)\n", failures, warnings)
	}
	return b.String()
}

func (r Report) counts() (failures, warnings int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusFail:
			failures++
		case StatusWarn:
			warnings++
		case StatusOK, StatusSkip:
		}
	}
	return failures, warnings
}

// glyph maps a status to a single terminal cell.
func glyph(s Status) string {
	switch s {
	case StatusOK:
		return "✓"
	case StatusWarn:
		return "!"
	case StatusFail:
		return "✗"
	case StatusSkip:
		return "-"
	}
	return "?"
}

// Run executes every diagnostic in order.
func Run(ctx context.Context, cfg config.Config) (Report, error) {
	var report Report

	dataDir, err := cfg.DataDir()
	if err != nil {
		return Report{}, fmt.Errorf("resolve data dir: %w", err)
	}
	report.Checks = append(report.Checks, checkDataDir(dataDir))
	report.Checks = append(report.Checks, checkConfig(cfg))
	report.Checks = append(report.Checks, checkDocker(ctx))
	report.Checks = append(report.Checks, checkGateway(ctx, cfg))
	report.Checks = append(report.Checks, checkSecrets(cfg))

	return report, nil
}

// checkDataDir verifies the data directory exists or can be created.
func checkDataDir(dir string) Check {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return Check{
				Name:   "data directory",
				Status: StatusFail,
				Detail: dir + " exists but is not a directory",
				Hints:  []string{"remove the file or choose another --data-dir"},
			}
		}
		return Check{Name: "data directory", Status: StatusOK, Detail: dir}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Check{
			Name:   "data directory",
			Status: StatusFail,
			Detail: fmt.Sprintf("%s: %v", dir, err),
		}
	}

	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return Check{
			Name:   "data directory",
			Status: StatusFail,
			Detail: fmt.Sprintf("cannot create %s: %v", dir, mkErr),
			Hints:  []string{"create the directory manually or pass --data-dir"},
		}
	}
	return Check{Name: "data directory", Status: StatusOK, Detail: dir + " (created)"}
}

// checkConfig validates the loaded configuration.
func checkConfig(cfg config.Config) Check {
	if err := cfg.Validate(); err != nil {
		return Check{
			Name:   "configuration",
			Status: StatusFail,
			Detail: "invalid",
			Hints:  []string{err.Error()},
		}
	}
	return Check{
		Name:   "configuration",
		Status: StatusOK,
		Detail: fmt.Sprintf("%d provider(s), %d profile(s)", len(cfg.Providers), len(cfg.ModelProfiles)),
	}
}

// checkDocker verifies the Docker CLI is reachable and the daemon answers.
func checkDocker(ctx context.Context) Check {
	path, err := exec.LookPath("docker")
	if err != nil {
		return Check{
			Name:   "docker",
			Status: StatusFail,
			Detail: "docker CLI not found on PATH",
			Hints:  []string{"install Docker Desktop and ensure it is running", "tool execution is impossible without Docker"},
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, path, "version", "--format", "{{.Server.Version}}")
	out, err := cmd.Output()
	if err != nil {
		return Check{
			Name:   "docker",
			Status: StatusFail,
			Detail: "daemon not reachable",
			Hints:  []string{"start Docker Desktop", "verify the Linux container engine is enabled"},
		}
	}
	return Check{Name: "docker", Status: StatusOK, Detail: "server " + strings.TrimSpace(string(out))}
}

// checkGateway probes the configured LLM gateway health endpoint.
func checkGateway(ctx context.Context, cfg config.Config) Check {
	provider, ok := cfg.Providers["9router"]
	if !ok {
		for _, candidate := range cfg.Providers {
			provider = candidate
			ok = true
			break
		}
	}
	if !ok {
		return Check{
			Name:   "llm gateway",
			Status: StatusFail,
			Detail: "no provider configured",
			Hints:  []string{"add a provider block to config.yaml"},
		}
	}

	if provider.Kind != "openai_compatible" {
		return Check{
			Name:   "llm gateway",
			Status: StatusSkip,
			Detail: "provider kind " + provider.Kind + " has no health endpoint",
		}
	}

	healthURL := strings.TrimSuffix(provider.BaseURL, "/v1") + "/api/health"
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return Check{Name: "llm gateway", Status: StatusFail, Detail: fmt.Sprintf("bad health URL: %v", err)}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Check{
			Name:   "llm gateway",
			Status: StatusFail,
			Detail: fmt.Sprintf("%s unreachable", healthURL),
			Hints:  []string{"start the gateway", "check providers.*.base_url in config.yaml"},
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Check{
			Name:   "llm gateway",
			Status: StatusFail,
			Detail: fmt.Sprintf("%s returned %d", healthURL, resp.StatusCode),
		}
	}
	return Check{Name: "llm gateway", Status: StatusOK, Detail: "healthy at " + healthURL}
}

// checkSecrets verifies that each secret-bearing environment variable is set,
// without ever printing a value.
func checkSecrets(cfg config.Config) Check {
	names := cfg.SecretEnvNames()
	if len(names) == 0 {
		return Check{Name: "secrets", Status: StatusSkip, Detail: "no secret environment variables declared"}
	}

	var missing []string
	for _, name := range names {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Check{
			Name:   "secrets",
			Status: StatusWarn,
			Detail: "unset: " + strings.Join(missing, ", "),
			Hints:  []string{"optional sources are skipped when their key is absent"},
		}
	}
	return Check{Name: "secrets", Status: StatusOK, Detail: fmt.Sprintf("%d variable(s) set", len(names))}
}

// StorePath is the database location for a data directory. Exported so the CLI
// and the app agree on one definition.
func StorePath(dataDir string) string {
	return filepath.Join(dataDir, "cosmics.db")
}
