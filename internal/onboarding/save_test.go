package onboarding_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/onboarding"
)

// wizardConfig builds a valid configuration the way the wizard would.
func wizardConfig(t *testing.T) config.Config {
	t.Helper()

	cfg, err := onboarding.NewState(onboarding.DefaultEndpoint).
		Choose("astral-projection").
		BuildConfig()
	require.NoError(t, err)
	return cfg
}

// Windows has no POSIX mode bits: it reports 0666/0777 however the file was
// created, so the exact owner-only bits are asserted only where they exist.
func wantFilePerm() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0o666
	}
	return 0o600
}

func wantDirPerm() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0o777
	}
	return 0o700
}

func Test_Save_writes_the_file_with_0600_permissions_when_the_path_is_new(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := wizardConfig(t)

	// When
	err := onboarding.Save(context.Background(), cfg, path)

	// Then
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())
	assert.Equal(t, wantFilePerm(), info.Mode().Perm(),
		"the file names provider endpoints and env vars, so it is owner-only")
}

func Test_Save_writes_loadable_yaml_when_the_config_was_valid(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := wizardConfig(t)

	// When
	require.NoError(t, onboarding.Save(context.Background(), cfg, path))
	reloaded, err := config.Load(context.Background(), path)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "astral-projection", reloaded.ModelProfiles["default"]["recon"][0].Model)
	assert.Equal(t, onboarding.DefaultAPIKeyEnv, reloaded.Providers["9router"].APIKeyEnv)
}

func Test_Save_refuses_to_overwrite_when_Overwrite_is_false(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("original: true\n"), 0o600))
	cfg := wizardConfig(t)

	// When
	err := onboarding.Save(context.Background(), cfg, path)

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, onboarding.ErrConfigExists)

	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "original: true\n", string(raw), "the existing file must be untouched")
}

func Test_Save_overwrites_when_Overwrite_is_true(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("original: true\n"), 0o600))
	cfg := wizardConfig(t)

	// When
	err := onboarding.SaveWithOptions(context.Background(), cfg, path, onboarding.SaveOptions{Overwrite: true})

	// Then
	require.NoError(t, err)

	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.NotContains(t, string(raw), "original: true")
	assert.Contains(t, string(raw), "astral-projection")
}

func Test_Save_leaves_no_temp_file_when_the_rename_succeeds(t *testing.T) {
	// Given
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := wizardConfig(t)

	// When
	require.NoError(t, onboarding.Save(context.Background(), cfg, path))

	// Then
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the final file may remain")
	assert.Equal(t, "config.yaml", entries[0].Name())
}

func Test_Save_creates_the_parent_directory_when_it_is_missing(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "nested", "deeper", "config.yaml")
	cfg := wizardConfig(t)

	// When
	err := onboarding.Save(context.Background(), cfg, path)

	// Then
	require.NoError(t, err)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr)
}

func Test_Save_returns_an_error_when_the_path_is_empty(t *testing.T) {
	// Given
	cfg := wizardConfig(t)

	// When
	err := onboarding.Save(context.Background(), cfg, "")

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, config.ErrInvalidConfig)
}

func Test_Save_respects_a_cancelled_context(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "config.yaml")

	// When
	err := onboarding.Save(ctx, wizardConfig(t), path)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func Test_DefaultPath_creates_the_directory_with_0700_permissions(t *testing.T) {
	// Given
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// When
	path, err := onboarding.DefaultPath()

	// Then
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".cosmics", "config.yaml"), path)

	info, statErr := os.Stat(filepath.Join(home, ".cosmics"))
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
	assert.Equal(t, wantDirPerm(), info.Mode().Perm(),
		"the data directory is owner-only")
}

func Test_DefaultPath_is_idempotent_when_the_directory_already_exists(t *testing.T) {
	// Given
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	first, err := onboarding.DefaultPath()
	require.NoError(t, err)

	// When
	second, err := onboarding.DefaultPath()

	// Then
	require.NoError(t, err)
	assert.Equal(t, first, second)
}
