package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/store"
)

// assetFixture builds a valid asset for an engagement.
func assetFixture(engagementID domain.ID, kind, value string, seenAt domain.TimestampMs) store.Asset {
	return store.Asset{
		EngagementID: engagementID,
		Kind:         kind,
		Value:        value,
		Display:      value,
		State:        "unknown",
		Confidence:   0.9,
		FirstSeenAt:  seenAt,
		LastSeenAt:   seenAt,
		Source:       "osint:crtsh",
		AttrsJSON:    `{"discovered_via":"certificate_transparency"}`,
	}
}

func Test_AssetRepo_Upsert_keeps_the_original_id_when_the_same_asset_is_seen_twice(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "asset-id-stable")
	repo := store.NewAssetRepo(db)
	asset := assetFixture(engagement.ID, "subdomain", "api.acme.test", baseTimestamp)

	first, err := repo.Upsert(ctx, asset)
	require.NoError(t, err)

	// When the same (engagement, kind, value) is observed again
	second, err := repo.Upsert(ctx, assetFixture(engagement.ID, "subdomain", "api.acme.test", baseTimestamp+1000))

	// Then
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "the canonical id must survive a re-observation")
	assert.Equal(t, first.FirstSeenAt, second.FirstSeenAt, "first_seen_at is history, not a value to refresh")
}

func Test_AssetRepo_Upsert_updates_last_seen_at_when_the_asset_reappears(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "asset-last-seen")
	repo := store.NewAssetRepo(db)
	asset := assetFixture(engagement.ID, "subdomain", "www.acme.test", baseTimestamp)
	first, err := repo.Upsert(ctx, asset)
	require.NoError(t, err)

	reappeared := assetFixture(engagement.ID, "subdomain", "www.acme.test", baseTimestamp+5000)
	reappeared.State = "alive"
	reappeared.Confidence = 0.5

	// When
	second, err := repo.Upsert(ctx, reappeared)

	// Then
	require.NoError(t, err)
	assert.Greater(t, second.LastSeenAt, first.LastSeenAt, "last_seen_at must advance")
	assert.Equal(t, "alive", second.State)
	assert.InDelta(t, 0.5, second.Confidence, 1e-9)
}

func Test_AssetRepo_List_is_sorted_when_several_kinds_exist(t *testing.T) {
	// Given assets inserted out of order
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "asset-ordering")
	repo := store.NewAssetRepo(db)

	for _, fixture := range []struct{ kind, value string }{
		{"url", "https://acme.test/z"},
		{"domain", "acme.test"},
		{"subdomain", "b.acme.test"},
		{"subdomain", "a.acme.test"},
		{"ip", "203.0.113.9"},
	} {
		_, err := repo.Upsert(ctx, assetFixture(engagement.ID, fixture.kind, fixture.value, baseTimestamp))
		require.NoError(t, err)
	}

	// When
	assets, err := repo.List(ctx, engagement.ID)

	// Then
	require.NoError(t, err)
	require.Len(t, assets, 5)
	got := make([]string, 0, len(assets))
	for _, asset := range assets {
		got = append(got, asset.Kind+"/"+asset.Value)
	}
	assert.Equal(t, []string{
		"domain/acme.test",
		"ip/203.0.113.9",
		"subdomain/a.acme.test",
		"subdomain/b.acme.test",
		"url/https://acme.test/z",
	}, got)
}

func Test_AssetRepo_AddService_rejects_a_port_out_of_range_when_given_zero(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "service-port")
	repo := store.NewAssetRepo(db)
	asset, err := repo.Upsert(ctx, assetFixture(engagement.ID, "ip", "203.0.113.7", baseTimestamp))
	require.NoError(t, err)

	// When
	addErr := repo.AddService(ctx, store.Service{
		AssetID:   asset.ID,
		Port:      0,
		Protocol:  "tcp",
		State:     "open",
		UpdatedAt: baseTimestamp,
	})

	// Then the bad field is named
	require.Error(t, addErr)
	require.ErrorIs(t, addErr, store.ErrInvalidAsset)
	assert.Contains(t, addErr.Error(), "port")
}

func Test_AssetRepo_CountByKind_counts_only_the_named_kind(t *testing.T) {
	// Given
	ctx := context.Background()
	db := openTestDB(t)
	engagement := seedEngagement(t, db, "asset-count")
	repo := store.NewAssetRepo(db)

	for _, fixture := range []struct{ kind, value string }{
		{"subdomain", "a.acme.test"},
		{"subdomain", "b.acme.test"},
		{"domain", "acme.test"},
	} {
		_, err := repo.Upsert(ctx, assetFixture(engagement.ID, fixture.kind, fixture.value, baseTimestamp))
		require.NoError(t, err)
	}

	// When
	subdomains, err := repo.CountByKind(ctx, engagement.ID, "subdomain")
	require.NoError(t, err)
	total, err := repo.CountByKind(ctx, engagement.ID, "")
	require.NoError(t, err)

	// Then
	assert.Equal(t, 2, subdomains)
	assert.Equal(t, 3, total, "an empty kind counts every asset")
}
