package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/config"
)

func Test_onboardFirstRun_writes_a_config_file_when_the_wizard_completes(t *testing.T) {
	// Given
	dir := t.TempDir()
	input := strings.NewReader("\n\nmanual-model\n\ny\n")
	var out bytes.Buffer

	// When
	cfg, err := onboardFirstRun(context.Background(), dir, input, &out, nil)

	// Then
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.Equal(t, dir, cfg.App.DataDir)

	path := filepath.Join(dir, "config.yaml")
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "the wizard result must be persisted")

	reloaded, loadErr := config.Load(context.Background(), path)
	require.NoError(t, loadErr)
	assert.Equal(t, "manual-model", reloaded.ModelProfiles["default"]["recon"][0].Model)
	assert.Contains(t, out.String(), path)
}

func Test_onboardFirstRun_returns_ErrMissingConfig_when_the_operator_aborts(t *testing.T) {
	// Given
	dir := t.TempDir()

	// When
	_, err := onboardFirstRun(context.Background(), dir, strings.NewReader(""), new(bytes.Buffer), nil)

	// Then
	require.Error(t, err)
	require.ErrorIs(t, err, ErrMissingConfig)
	require.NotErrorIs(t, err, os.ErrNotExist)
}

func Test_onboardFirstRun_leaves_no_file_when_the_operator_aborts(t *testing.T) {
	// Given
	dir := t.TempDir()

	// When
	_, err := onboardFirstRun(context.Background(), dir, strings.NewReader("\n\n"), new(bytes.Buffer), nil)
	require.Error(t, err)

	// Then
	_, statErr := os.Stat(filepath.Join(dir, "config.yaml"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func Test_hasUsableProvider_reports_true_when_an_endpoint_is_set(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_URL", "http://localhost:20128/v1")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// When
	usable := hasUsableProvider(config.Default())

	// Then
	assert.True(t, usable, "an operator who set NINEROUTER_URL already opted in")
}

func Test_hasUsableProvider_reports_true_when_only_the_key_is_set(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_URL", "")
	t.Setenv("NINEROUTER_KEY", "sk-present")

	// When
	usable := hasUsableProvider(config.Default())

	// Then
	assert.True(t, usable)
}

func Test_hasUsableProvider_reports_false_when_nothing_is_configured(t *testing.T) {
	// Given
	t.Setenv("NINEROUTER_URL", "")
	t.Setenv("NINEROUTER_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// When
	usable := hasUsableProvider(config.Default())

	// Then
	assert.False(t, usable,
		"with nothing configured the silent default is exactly what setup must replace")
}

func Test_confirmOverwrite_returns_true_when_the_path_is_new(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")

	// When
	ok, err := confirmOverwrite(strings.NewReader(""), new(bytes.Buffer), path, false)

	// Then
	require.NoError(t, err)
	assert.True(t, ok, "a fresh path needs no confirmation")
}

func Test_confirmOverwrite_returns_true_when_forced(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	// When
	ok, err := confirmOverwrite(strings.NewReader(""), new(bytes.Buffer), path, true)

	// Then
	require.NoError(t, err)
	assert.True(t, ok)
}

func Test_confirmOverwrite_returns_false_when_the_operator_answers_no(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	// When
	ok, err := confirmOverwrite(strings.NewReader("n\n"), new(bytes.Buffer), path, false)

	// Then
	require.NoError(t, err)
	assert.False(t, ok)
}

func Test_confirmOverwrite_returns_true_when_the_operator_answers_yes(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	// When
	ok, err := confirmOverwrite(strings.NewReader("yes\n"), new(bytes.Buffer), path, false)

	// Then
	require.NoError(t, err)
	assert.True(t, ok)
}

func Test_confirmOverwrite_treats_eof_as_no(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	// When
	ok, err := confirmOverwrite(strings.NewReader(""), new(bytes.Buffer), path, false)

	// Then
	require.NoError(t, err)
	assert.False(t, ok, "a closed stdin must not silently overwrite")
}

func Test_setupTarget_prefers_an_explicit_path_when_given(t *testing.T) {
	// Given
	explicit := filepath.Join(t.TempDir(), "custom.yaml")

	// When
	path, err := setupTarget(explicit)

	// Then
	require.NoError(t, err)
	assert.Equal(t, explicit, path)
}

func Test_setupTarget_falls_back_to_the_default_path_when_none_is_given(t *testing.T) {
	// Given
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// When
	path, err := setupTarget("")

	// Then
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".cosmics", "config.yaml"), path)
}

func Test_Run_dispatches_setup_when_the_command_is_given(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "config.yaml")

	// When
	err := Run([]string{"-config", path, "setup", path})

	// Then
	require.Error(t, err, "stdin is not a terminal in this process, so the wizard aborts")
	assert.Contains(t, err.Error(), "setup", "the error must come from the setup path, not an unknown command")
	assert.NotErrorIs(t, err, ErrUnknownCommand)
}

func Test_knownCommand_recognises_every_dispatched_command(t *testing.T) {
	// Given
	known := []string{"", "setup", "doctor", "db"}

	// When / Then
	for _, command := range known {
		assert.True(t, knownCommand(command), "%q must be dispatched", command)
	}
	assert.False(t, knownCommand("nope"))
}
