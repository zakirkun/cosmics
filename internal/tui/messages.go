package tui

import "github.com/cosmics/cosmics/internal/domain"

// EngagementsLoadedMsg carries a fresh engagement list from the application.
//
// The TUI never queries the store itself: whatever a load returns, success or
// failure, arrives through this message and is rendered as-is.
type EngagementsLoadedMsg struct {
	Engagements []domain.Engagement
	Err         error
}

// ErrorMsg reports a background failure that is not tied to a data load.
type ErrorMsg struct {
	Err error
}
