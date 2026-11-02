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

// EngagementDraftedMsg carries a validated engagement out of the new-engagement
// form.
//
// The form does not persist. It hands the engagement upward so the application
// owns the write, which keeps the TUI free of a database dependency and lets a test
// assert the drafted value without a store.
type EngagementDraftedMsg struct {
	Engagement domain.Engagement
}
