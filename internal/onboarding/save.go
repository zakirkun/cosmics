package onboarding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/cosmics/cosmics/internal/config"
)

// ErrConfigExists is returned when a save would clobber an existing file.
var ErrConfigExists = errors.New("onboarding: configuration file already exists")

// File modes. The file names provider endpoints and env var names, so it is
// owner-only; the directory matches the data-directory convention.
const (
	fileMode   = 0o600
	dirMode    = 0o700
	tempSuffix = ".tmp"
)

// SaveOptions tunes Save.
type SaveOptions struct {
	Overwrite bool
}

// Save writes cfg to path as YAML, atomically and with owner-only permissions.
//
// The file is written to path+".tmp", flushed to disk, then renamed, so a crash
// mid-write cannot leave a half-parsed configuration behind.
func Save(ctx context.Context, cfg config.Config, path string) error {
	return SaveWithOptions(ctx, cfg, path, SaveOptions{})
}

// SaveWithOptions is Save with an explicit overwrite decision.
func SaveWithOptions(ctx context.Context, cfg config.Config, path string, opts SaveOptions) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if path == "" {
		return fmt.Errorf("save config: empty path: %w", config.ErrInvalidConfig)
	}

	if !opts.Overwrite {
		_, err := os.Stat(path)
		switch {
		case err == nil:
			return fmt.Errorf("%s: %w", path, ErrConfigExists)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("stat %s: %w", path, err)
		}
	}

	payload, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	tempPath := path + tempSuffix
	if err := writeAndSync(tempPath, payload); err != nil {
		return err
	}

	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename %s to %s: %w", tempPath, path, err)
	}
	return nil
}

// writeAndSync writes payload to path and flushes it before returning.
func writeAndSync(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}

	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// DefaultPath returns ~/.cosmics/config.yaml, creating the directory with 0700
// so the subsequent save has somewhere safe to write.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	dir := filepath.Join(home, ".cosmics")
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	return filepath.Join(dir, "config.yaml"), nil
}
