package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/onboarding"
)

// runSetup runs the wizard unconditionally and saves the result. It is the
// `cosmics setup` command: an operator re-runs it to change the gateway or the
// model after first launch, so it always offers to replace the existing file.
func runSetup(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(out)
	forced := flags.Bool("force", false, "replace an existing configuration without asking")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse setup flags: %w", err)
	}

	path, err := setupTarget(flags.Arg(0))
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	ok, err := confirmOverwrite(reader, out, path, *forced)
	if err != nil {
		return err
	}
	if !ok {
		_, writeErr := fmt.Fprintf(out, "Left %s untouched.\n", path)
		return writeErr
	}

	cfg, err := runWizard(ctx, reader, out, path)
	if err != nil {
		return fmt.Errorf("run setup: %w", err)
	}

	cfg.App.DataDir = filepath.Dir(path)
	if err := onboarding.SaveWithOptions(ctx, cfg, path, onboarding.SaveOptions{Overwrite: true}); err != nil {
		return fmt.Errorf("save configuration: %w", err)
	}

	_, err = fmt.Fprintf(out, "Wrote %s\n", path)
	return err
}

// runWizard drives onboarding, preferring the interactive TUI.
//
// The TUI needs a terminal on stdin. When the command is piped, bubbletea cannot
// read keys, so the line-oriented flow is used instead; both paths produce the
// same validated configuration and differ only in how it is collected.
func runWizard(ctx context.Context, reader *bufio.Reader, out io.Writer, path string) (config.Config, error) {
	if !isTerminal(os.Stdin) {
		return onboarding.Interactive(ctx, reader, out, onboarding.GatewayDiscoverer{})
	}

	dir := filepath.Dir(path)

	saved, err := onboarding.Run(ctx, onboarding.TUIOptions{
		DefaultEndpoint: onboarding.DefaultEndpoint,
		Discover:        onboarding.GatewayDiscoverer{},
		Save: func(saveCtx context.Context, cfg config.Config) (string, error) {
			cfg.App.DataDir = dir
			if saveErr := onboarding.SaveWithOptions(saveCtx, cfg, path, onboarding.SaveOptions{Overwrite: true}); saveErr != nil {
				return "", saveErr
			}
			return path, nil
		},
	})
	if err != nil {
		return config.Config{}, err
	}

	loaded, err := config.Load(ctx, saved)
	if err != nil {
		return config.Config{}, fmt.Errorf("reload written config: %w", err)
	}
	return loaded, nil
}

// isTerminal reports whether file is an interactive terminal we may hand to
// bubbletea.
//
// ModeCharDevice alone is not enough: under `go test` on Windows, os.Stdin can
// still be a character device, and bubbletea would then block forever waiting for
// keys that never arrive. A test binary is therefore treated as non-interactive,
// which is also the behaviour a caller running the tests expects.
func isTerminal(file *os.File) bool {
	if strings.HasSuffix(os.Args[0], ".test") || strings.HasSuffix(os.Args[0], ".test.exe") {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// setupTarget resolves the destination, preferring an explicit argument.
func setupTarget(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	path, err := onboarding.DefaultPath()
	if err != nil {
		return "", fmt.Errorf("resolve configuration path: %w", err)
	}
	return path, nil
}

// onboardFirstRun runs the wizard and writes the result into dataDir.
//
// It replaces the silent environment fallback: a first run now guides the
// operator to a real file instead of guessing a gateway.
func onboardFirstRun(ctx context.Context, dataDir string, in io.Reader, out io.Writer, discoverer onboarding.Discoverer) (config.Config, error) {
	cfg, err := onboarding.Interactive(ctx, in, out, discoverer)
	if err != nil {
		if errors.Is(err, onboarding.ErrAborted) {
			return config.Config{}, fmt.Errorf("no configuration file and setup was aborted: %w", ErrMissingConfig)
		}
		return config.Config{}, fmt.Errorf("first-run setup: %w", err)
	}

	cfg.App.DataDir = dataDir
	path := filepath.Join(dataDir, "config.yaml")

	if err := onboarding.SaveWithOptions(ctx, cfg, path, onboarding.SaveOptions{}); err != nil {
		return config.Config{}, fmt.Errorf("save first-run configuration: %w", err)
	}
	if _, err := fmt.Fprintf(out, "Wrote %s\n", path); err != nil {
		return config.Config{}, fmt.Errorf("report configuration path: %w", err)
	}
	return cfg, nil
}

// hasUsableProvider reports whether the environment already supplied a gateway.
//
// Nothing set means the operator never opted in, so the hardcoded default is
// exactly the guess the wizard exists to replace.
func hasUsableProvider(cfg config.Config) bool {
	if os.Getenv("NINEROUTER_URL") != "" || os.Getenv("ANTHROPIC_API_KEY") != "" {
		return true
	}
	for _, provider := range cfg.Providers {
		if provider.APIKeyEnv != "" && os.Getenv(provider.APIKeyEnv) != "" {
			return true
		}
	}
	return false
}

// confirmOverwrite asks before clobbering an existing file. A fresh path needs
// no confirmation and -force skips the question.
func confirmOverwrite(in io.Reader, out io.Writer, path string, forced bool) (bool, error) {
	if forced {
		return true, nil
	}

	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}

	if _, err := fmt.Fprintf(out, "%s already exists. Replace it? [y/N]: ", path); err != nil {
		return false, fmt.Errorf("write prompt: %w", err)
	}

	reader, ok := in.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReader(in)
	}

	line, readErr := reader.ReadString('\n')
	if readErr != nil && strings.TrimSpace(line) == "" {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
