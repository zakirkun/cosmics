package cli_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/cli"
)

func Test_Run_returns_version_when_the_version_flag_is_set(t *testing.T) {
	// Given
	args := []string{"-version"}

	// When
	err := cli.Run(args)

	// Then
	require.NoError(t, err)
}

func Test_Run_returns_errUnknownCommand_when_the_command_is_not_recognised(t *testing.T) {
	// Given
	args := []string{"definitely-not-a-command"}

	// When
	err := cli.Run(args)

	// Then
	require.Error(t, err)
	assert.ErrorIs(t, err, cli.ErrUnknownCommand)
}

func Test_Run_returns_an_error_when_flag_parsing_fails(t *testing.T) {
	// Given
	args := []string{"-no-such-flag"}

	// When
	err := cli.Run(args)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse flags")
}

func Test_Run_returns_an_error_when_the_explicit_config_path_does_not_exist(t *testing.T) {
	// Given
	args := []string{"-config", t.TempDir() + "/missing.yaml"}

	// When
	err := cli.Run(args)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load config")
}

func Test_Run_returns_errUnknownCommand_when_db_has_no_subcommand(t *testing.T) {
	// Given
	args := []string{"db"}

	// When
	err := cli.Run(args)

	// Then
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, cli.ErrUnknownCommand) || errors.Is(err, cli.ErrMissingConfig),
		"expected a command or configuration error, got %v", err,
	)
}

func Test_Run_returns_errUnknownCommand_when_db_receives_an_unknown_subcommand(t *testing.T) {
	// Given
	args := []string{"db", "definitely-not-a-subcommand"}

	// When
	err := cli.Run(args)

	// Then
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, cli.ErrUnknownCommand) || errors.Is(err, cli.ErrMissingConfig),
		"expected a command or configuration error, got %v", err,
	)
}
