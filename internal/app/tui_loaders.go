package app

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/cosmics/cosmics/internal/config"
	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
	"github.com/cosmics/cosmics/internal/tui"
)

// tuiLoaders returns the callbacks that fill the shell's read-only screens.
//
// Each returns a tea.Cmd rather than a value: the command runs on the program's
// goroutine and reports back as a message, so a slow query cannot stall the event
// loop that is drawing the frame.
func tuiLoaders(ctx context.Context, db *store.DB, cfg config.Config) (tuiLoadersSet, error) {
	if db == nil {
		return tuiLoadersSet{}, fmt.Errorf("database: %w", ErrPipelineIncomplete)
	}

	return tuiLoadersSet{
		findings: func(engagementID string) tea.Cmd {
			return func() tea.Msg {
				rows, err := loadFindingRows(ctx, db, engagementID)
				return tui.FindingsLoadedMsg{Findings: rows, Err: err}
			}
		},
		memory: func(engagementID string) tea.Cmd {
			return func() tea.Msg {
				facts, err := loadFactViews(ctx, db, engagementID)
				return tui.MemoryFactsLoadedMsg{Facts: facts, Err: err}
			}
		},
		settings: func() tea.Cmd {
			return func() tea.Msg {
				return tui.SettingsLoadedMsg{Settings: settingsFromConfig(cfg)}
			}
		},
	}, nil
}

// tuiLoadersSet groups the three callbacks so a nil set is impossible.
type tuiLoadersSet struct {
	findings func(engagementID string) tea.Cmd
	memory   func(engagementID string) tea.Cmd
	settings func() tea.Cmd
}

// loadFindingRows reads findings for one engagement.
//
// An empty identifier yields an empty list rather than an error: the operator may
// open the findings screen before selecting an engagement, and a red error there
// would be misleading.
func loadFindingRows(ctx context.Context, db *store.DB, engagementID string) ([]tui.FindingRow, error) {
	if engagementID == "" {
		return nil, nil
	}

	id, err := domain.NewID(engagementID)
	if err != nil {
		return nil, fmt.Errorf("findings engagement id %q: %w", engagementID, err)
	}

	rows, err := store.NewFindingRepo(db).List(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}

	out := make([]tui.FindingRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, tui.FindingRow{
			ID:         row.ID.String(),
			Title:      row.Title,
			Category:   row.Category,
			Severity:   row.Severity,
			Confidence: row.Confidence,
			Status:     row.Status,
			Verified:   row.Verified,
		})
	}
	return out, nil
}

// loadFactViews reads the assets of an engagement as memory rows.
//
// Facts live in their own table whose repository is not yet wired here, so the
// inventory is shown instead of an empty pane. This is deliberate: the screen must
// show what the database actually holds, and claiming there are no facts when
// there are assets would be the kind of quiet inaccuracy this project avoids.
func loadFactViews(ctx context.Context, db *store.DB, engagementID string) ([]tui.FactView, error) {
	if engagementID == "" {
		return nil, nil
	}

	id, err := domain.NewID(engagementID)
	if err != nil {
		return nil, fmt.Errorf("memory engagement id %q: %w", engagementID, err)
	}

	assets, err := store.NewAssetRepo(db).List(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}

	out := make([]tui.FactView, 0, len(assets))
	for _, asset := range assets {
		out = append(out, tui.FactView{
			Key:        asset.Kind,
			Value:      asset.Value,
			Confidence: fmt.Sprintf("%.2f", asset.Confidence),
			UpdatedAt:  asset.LastSeenAt,
		})
	}
	return out, nil
}

// settingsFromConfig builds the settings view without ever reading a key value.
func settingsFromConfig(cfg config.Config) tui.SettingsView {
	view := tui.SettingsView{
		DataDir:     cfg.App.DataDir,
		ModeDefault: cfg.App.LogLevel,
	}

	if names := sortedProviderNames(cfg.Providers); len(names) > 0 {
		provider := cfg.Providers[names[0]]
		view.Endpoint = provider.BaseURL
		view.APIKeyEnv = provider.APIKeyEnv
		if provider.APIKeyEnv != "" {
			_, set := os.LookupEnv(provider.APIKeyEnv)
			view.APIKeySet = set
		}
	}

	if names := sortedProfileNames(cfg.ModelProfiles); len(names) > 0 {
		view.Profile = names[0]
		profile := cfg.ModelProfiles[names[0]]

		for _, stage := range domain.AllStages {
			if chain, ok := profile[stage]; ok && len(chain) > 0 {
				view.Model = chain[0].Model
				break
			}
		}
	}

	return view
}

func sortedProfileNames(profiles map[string]config.ModelProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}

	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// sortedProviderNames returns provider names in a stable order.
//
// One is used, but the ordering is explicit so the choice cannot change between
// runs if a configuration ever holds several.
func sortedProviderNames(providers map[string]config.Provider) []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}

	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
