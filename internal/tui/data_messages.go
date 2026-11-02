package tui

// This file is the seam between persistence and rendering.
//
// The shell depends on internal/domain only: that rule is what lets every
// renderer here stay a pure function of a typed value. The messages below
// therefore carry view models, not store rows; the application maps a stored
// row at the call site, then sends the result. No import of internal/store or
// internal/memory appears anywhere in this package.

// FindingsLoadedMsg carries a fresh findings list into the shell.
type FindingsLoadedMsg struct {
	Findings []FindingRow
	Err      error
}

// MemoryFactsLoadedMsg carries the active facts of the selected engagement.
type MemoryFactsLoadedMsg struct {
	Facts []FactView
	Err   error
}

// SettingsLoadedMsg carries the effective configuration shown by the settings
// screen. It never carries a secret: only whether the environment variable is
// set.
type SettingsLoadedMsg struct {
	Settings SettingsView
	Err      error
}
