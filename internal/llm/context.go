package llm

import (
	"context"

	"github.com/cosmics/cosmics/internal/domain"
)

// ctxKey is an unexported struct so no other package can collide with these keys.
type ctxKey struct{ name string }

var (
	stageKey   = ctxKey{"stage"}
	purposeKey = ctxKey{"purpose"}
)

// WithStage attaches the stage a call belongs to, so the router can persist it
// without every caller threading it through the provider signatures.
func WithStage(ctx context.Context, stage domain.StageKind) context.Context {
	return context.WithValue(ctx, stageKey, stage)
}

// WithPurpose attaches the reason a call is being made.
func WithPurpose(ctx context.Context, purpose Purpose) context.Context {
	return context.WithValue(ctx, purposeKey, purpose)
}

// StageFrom reads the stage attached by WithStage.
func StageFrom(ctx context.Context) (domain.StageKind, bool) {
	stage, ok := ctx.Value(stageKey).(domain.StageKind)
	return stage, ok
}

// PurposeFrom reads the purpose attached by WithPurpose.
func PurposeFrom(ctx context.Context) (Purpose, bool) {
	purpose, ok := ctx.Value(purposeKey).(Purpose)
	return purpose, ok
}

// domainTimestamp converts a router timestamp into the domain's millisecond type.
func domainTimestamp(ts Timestamp) domain.TimestampMs {
	return domain.TimestampMs(ts)
}
