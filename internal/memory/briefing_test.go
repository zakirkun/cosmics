package memory_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/memory"
	"github.com/cosmics/cosmics/internal/store"
)

// briefingEngagement builds a running engagement at a fixed instant.
func briefingEngagement(id domain.ID) domain.Engagement {
	return domain.Engagement{
		ID:              id,
		Name:            "Acme External Assessment",
		Client:          "Acme Corporation",
		Mode:            domain.ModeFull,
		Status:          domain.StatusRunning,
		Stage:           domain.EngageStageRecon,
		Profile:         "default",
		BudgetUSDMicros: 5_000_000,
		SpentUSDMicros:  310_000,
		TokenBudget:     200_000,
		TokensUsed:      42_000,
		CreatedAt:       baseTimestamp,
		UpdatedAt:       baseTimestamp,
		StartedAt:       baseTimestamp,
		ConfigJSON:      "{}",
	}
}

// seedBriefingData populates every table the briefing reads.
func seedBriefingData(t *testing.T, db *store.DB, engagementID domain.ID) {
	t.Helper()

	_, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO engagements
		 (id, name, client, mode, status, stage, authorized_by, authorized_at,
		  budget_usd_micros, spent_usd_micros, token_budget, tokens_used,
		  created_at, updated_at, started_at, config_json)
		 VALUES (?, 'Acme External Assessment', 'Acme Corporation', 'full', 'running', 'recon',
		         'dana.reyes', ?, 5000000, 310000, 200000, 42000, ?, ?, ?, '{}')`,
		engagementID.String(), int64(baseTimestamp),
		int64(baseTimestamp), int64(baseTimestamp), int64(baseTimestamp))
	require.NoError(t, err)

	_, err = db.Writer().ExecContext(t.Context(),
		`INSERT INTO engagement_targets (id, engagement_id, kind, value, created_at)
		 VALUES (?, ?, 'domain', 'example.com', ?)`,
		domain.NewULIDGenerator().NewAt(baseTimestamp).String(), engagementID.String(), int64(baseTimestamp))
	require.NoError(t, err)

	assetID := domain.NewULIDGenerator().NewAt(baseTimestamp).String()
	_, err = db.Writer().ExecContext(t.Context(),
		`INSERT INTO assets
		 (id, engagement_id, kind, value, state, confidence, first_seen_at, last_seen_at, source)
		 VALUES (?, ?, 'host', 'www.example.com', 'alive', 1.0, ?, ?, 'nmap')`,
		assetID, engagementID.String(), int64(baseTimestamp), int64(baseTimestamp))
	require.NoError(t, err)

	_, err = db.Writer().ExecContext(t.Context(),
		`INSERT INTO services (id, asset_id, port, protocol, state, service, product, version, updated_at)
		 VALUES (?, ?, 443, 'tcp', 'open', 'https', 'nginx', '1.24', ?)`,
		domain.NewULIDGenerator().NewAt(baseTimestamp).String(), assetID, int64(baseTimestamp))
	require.NoError(t, err)

	repo := memory.NewFactRepo(db)
	_, err = repo.Set(t.Context(), memory.SetParams{
		EngagementID: engagementID,
		Subject:      "www.example.com",
		Predicate:    "web.tech",
		Value:        "nginx 1.24, PHP 8.2",
		ValueType:    memory.ValueTypeText,
		Confidence:   0.9,
		SourceKind:   memory.SourceToolRun,
		Stage:        domain.StageRecon,
		Now:          baseTimestamp,
	})
	require.NoError(t, err)
	_, err = repo.Set(t.Context(), memory.SetParams{
		EngagementID: engagementID,
		Subject:      "app.example.com",
		Predicate:    "auth.mechanism",
		Value:        "form login, no MFA",
		ValueType:    memory.ValueTypeText,
		Confidence:   0.7,
		SourceKind:   memory.SourceLLMInference,
		Stage:        domain.StageAnalysis,
		Now:          baseTimestamp + 1_000,
	})
	require.NoError(t, err)

	_, err = db.Writer().ExecContext(t.Context(),
		`INSERT INTO findings
		 (id, engagement_id, title, category, severity, confidence, description, status,
		  created_at, updated_at)
		 VALUES (?, ?, 'SQL injection in /search', 'web', 'high', 'likely', 'x', 'open', ?, ?)`,
		domain.NewULIDGenerator().NewAt(baseTimestamp).String(), engagementID.String(),
		int64(baseTimestamp), int64(baseTimestamp))
	require.NoError(t, err)
	_, err = db.Writer().ExecContext(t.Context(),
		`INSERT INTO findings
		 (id, engagement_id, title, category, severity, confidence, description, status,
		  created_at, updated_at)
		 VALUES (?, ?, 'Outdated jQuery', 'web', 'medium', 'possible', 'x', 'open', ?, ?)`,
		domain.NewULIDGenerator().NewAt(baseTimestamp).String(), engagementID.String(),
		int64(baseTimestamp), int64(baseTimestamp))
	require.NoError(t, err)

	for i := range 3 {
		at := baseTimestamp + domain.TimestampMs(i)*1_000
		_, err = db.Writer().ExecContext(t.Context(),
			`INSERT INTO events (id, engagement_id, stage, kind, level, message, created_at)
			 VALUES (?, ?, 'recon', 'tool_end', 'info', ?, ?)`,
			domain.NewULIDGenerator().NewAt(at).String(), engagementID.String(),
			"nmap finished on example.com", int64(at))
		require.NoError(t, err)
	}
}

func Test_BuildBriefing_is_deterministic_when_called_twice_on_the_same_state(t *testing.T) {
	// Given: a database populated across every section the briefing reads.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedBriefingData(t, db, engagementID)
	deps := memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp + 60_000,
	}

	// When: the briefing is built twice from the same rows.
	first, err := memory.BuildBriefing(t.Context(), deps)
	require.NoError(t, err)
	second, err := memory.BuildBriefing(t.Context(), deps)
	require.NoError(t, err)

	// Then: the two renderings are byte-identical.
	assert.Equal(t, first, second, "the briefing must be a deterministic projection of the database")
	assert.NotEmpty(t, first)
}

func Test_BuildBriefing_is_deterministic_when_repeated_across_many_calls(t *testing.T) {
	// Given: a populated database and a stable dependency set.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedBriefingData(t, db, engagementID)
	deps := memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp + 60_000,
	}

	first, err := memory.BuildBriefing(t.Context(), deps)
	require.NoError(t, err)

	// When: it is rebuilt repeatedly.
	// Then: no call drifts, which a map iteration or an unsorted query would cause.
	for i := range 5 {
		again, err := memory.BuildBriefing(t.Context(), deps)
		require.NoError(t, err)
		assert.Equal(t, first, again, "call %d diverged", i)
	}
}

func Test_BuildBriefing_includes_every_section_when_data_exists(t *testing.T) {
	// Given: a populated database.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedBriefingData(t, db, engagementID)

	// When: the briefing is built.
	out, err := memory.BuildBriefing(t.Context(), memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp + 60_000,
	})

	// Then: the engagement, targets, assets, facts, findings, activity, and
	// budget blocks are all present, with their data.
	require.NoError(t, err)
	for _, heading := range []string{
		"## Engagement", "## Targets (1)", "## Assets (1", "## Facts (2 active, 0 superseded)",
		"## Findings (2: 1 high, 1 medium)", "## Recent activity (3 events)", "## Budget",
	} {
		assert.Contains(t, out, heading)
	}

	assert.Contains(t, out, "name: Acme External Assessment")
	assert.Contains(t, out, "mode: full")
	assert.Contains(t, out, "stage: recon")
	assert.Contains(t, out, "- example.com (domain)")
	assert.Contains(t, out, "host www.example.com")
	assert.Contains(t, out, "443/tcp https nginx 1.24")
	assert.Contains(t, out, "web.tech[www.example.com] = nginx 1.24, PHP 8.2")
	assert.Contains(t, out, "[high, likely] SQL injection in /search")
	assert.Contains(t, out, "nmap finished on example.com")
	assert.Contains(t, out, "tokens 42000/200000")
	assert.Contains(t, out, "cost $0.31/$5.00")
}

func Test_BuildBriefing_orders_findings_by_severity_when_severities_mix(t *testing.T) {
	// Given: findings inserted in ascending severity order.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)

	for _, severity := range []string{"info", "low", "medium", "critical"} {
		_, err := db.Writer().ExecContext(t.Context(),
			`INSERT INTO findings
			 (id, engagement_id, title, category, severity, confidence, description, status,
			  created_at, updated_at)
			 VALUES (?, ?, ?, 'web', ?, 'likely', 'x', 'open', ?, ?)`,
			domain.NewULIDGenerator().NewAt(baseTimestamp).String(), engagementID.String(),
			"Finding "+severity, severity, int64(baseTimestamp), int64(baseTimestamp))
		require.NoError(t, err)
	}

	// When: the briefing renders them.
	out, err := memory.BuildBriefing(t.Context(), memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp,
	})
	require.NoError(t, err)

	// Then: critical precedes low, which precedes info.
	critical := strings.Index(out, "Finding critical")
	low := strings.Index(out, "Finding low")
	info := strings.Index(out, "Finding info")

	require.Positive(t, critical)
	require.Positive(t, low)
	require.Positive(t, info)
	assert.Less(t, critical, low)
	assert.Less(t, low, info)
}

func Test_BuildBriefing_renders_empty_sections_when_no_data_exists(t *testing.T) {
	// Given: an engagement with no targets, assets, facts, findings, or events.
	db := openTestDB(t)
	engagementID := newEngagementID()
	seedEngagement(t, db, engagementID)

	// When: the briefing is built.
	out, err := memory.BuildBriefing(t.Context(), memory.BriefingDeps{
		DB:         db,
		Engagement: briefingEngagement(engagementID),
		Now:        baseTimestamp,
	})

	// Then: every section is still present, marked as empty.
	require.NoError(t, err)
	assert.Contains(t, out, "## Targets (0)")
	assert.Contains(t, out, "## Assets (0)")
	assert.Contains(t, out, "## Facts (0 active, 0 superseded)")
	assert.Contains(t, out, "## Findings (0)")
	assert.Contains(t, out, "## Recent activity (0 events)")
	assert.Contains(t, out, "(none)")
}
