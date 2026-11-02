package stages

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/osint"
	"github.com/cosmics/cosmics/internal/pipeline"
)

// ErrNoOSINTTarget is returned when the OSINT stage has nothing to query.
var ErrNoOSINTTarget = errors.New("stages: osint requires at least one target domain")

// OSINTDeps is everything the OSINT stage needs. There are no package-level
// globals: the composition root supplies a registry and the engagement targets.
type OSINTDeps struct {
	Assets   AssetRecorder
	Sources  *osint.Registry
	Targets  []domain.Target
	Clock    domain.Clock
	OnSource func(name string, err error)
}

// OSINTStage discovers public surface through the registered sources.
type OSINTStage struct {
	deps     OSINTDeps
	reporter []string
}

// NewOSINTStage validates the dependencies and returns the stage.
func NewOSINTStage(deps OSINTDeps) (*OSINTStage, error) {
	if deps.Assets == nil {
		return nil, fmt.Errorf("osint stage: asset recorder: %w", pipeline.ErrStageMissing)
	}
	if deps.Clock == nil {
		return nil, fmt.Errorf("osint stage: clock: %w", pipeline.ErrStageMissing)
	}
	if deps.Sources == nil {
		deps.Sources = osint.NewRegistry()
	}
	return &OSINTStage{deps: deps}, nil
}

// Kind implements pipeline.Stage.
func (s *OSINTStage) Kind() domain.StageKind { return domain.StageOSINT }

// LastSourceErrors reports the coverage gaps observed by the most recent Body
// call. It is a reporting aid only: Exit never reads it, because that would make
// completion depend on memory a restart loses.
func (s *OSINTStage) LastSourceErrors() []string {
	out := make([]string, len(s.reporter))
	copy(out, s.reporter)
	return out
}

// Enter requires at least one hostname-shaped target.
func (s *OSINTStage) Enter(_ context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: OSINT state", pipeline.ErrNoState)
	}
	hosts := s.targetHosts()
	if len(hosts) == 0 {
		return fmt.Errorf("%w: supply a domain, host, or URL target on engagement %q: %w",
			ErrNoOSINTTarget, state.Engagement.Name, ErrMissingPrerequisite)
	}
	return nil
}

// Body queries every enabled source for every target.
//
// A failing source is recorded and skipped: one dead third-party endpoint must
// not fail an engagement's whole OSINT pass. Records that fail normalisation are
// dropped with the same reporting, so hostile data never reaches persistence or
// an executor.
func (s *OSINTStage) Body(ctx context.Context, state *pipeline.State) error {
	if state == nil {
		return fmt.Errorf("%w: OSINT state", pipeline.ErrNoState)
	}
	hosts := s.targetHosts()
	if len(hosts) == 0 {
		return fmt.Errorf("%w: no usable target domain: %w", ErrNoOSINTTarget, ErrMissingPrerequisite)
	}

	s.reporter = s.reporter[:0]
	now := s.deps.Clock.Now()

	for _, source := range s.deps.Sources.All() {
		for _, host := range hosts {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("osint stage cancelled: %w", err)
			}
			records, err := source.Query(ctx, host)
			if err != nil {
				s.note(fmt.Sprintf("%s %s: %v", source.Name(), host, err))
				continue
			}
			if err := s.persist(ctx, state, source.Name(), records, now); err != nil {
				s.note(fmt.Sprintf("%s %s: %v", source.Name(), host, err))
			}
		}
	}
	return nil
}

// Exit is satisfied when at least one persistent asset is OSINT-sourced.
func (s *OSINTStage) Exit(ctx context.Context, state *pipeline.State) pipeline.ExitResult {
	if state == nil {
		return pipeline.Unmet("OSINT state is missing")
	}

	assets, err := s.deps.Assets.ListAssets(ctx, state.Engagement.ID)
	if err != nil {
		return pipeline.Unmet("OSINT assets for engagement %s could not be read: %v", state.Engagement.ID, err)
	}

	osintAssets := 0
	for _, asset := range assets {
		if strings.HasPrefix(asset.Source, OSINTSourcePrefix) {
			osintAssets++
		}
	}
	if osintAssets == 0 {
		return pipeline.Unmet(
			"no OSINT-sourced asset is persisted for engagement %s: %d assets exist, none from sources %s",
			state.Engagement.ID, len(assets), strings.Join(s.deps.Sources.Names(), ", "))
	}
	return pipeline.MetWith()
}

// persist converts and stores the records that survive normalisation.
func (s *OSINTStage) persist(
	ctx context.Context,
	state *pipeline.State,
	source string,
	records []osint.Record,
	now domain.TimestampMs,
) error {
	var rejected int
	for _, record := range records {
		if record.Source == "" {
			record.Source = source
		}
		asset, err := RecordToAsset(state.Engagement.ID, record, now)
		if err != nil {
			rejected++
			s.note(fmt.Sprintf("%s rejected record %q: %v", source, record.Value, err))
			continue
		}
		if _, err := s.deps.Assets.UpsertAsset(ctx, asset); err != nil {
			return fmt.Errorf("persist %s %q: %w", asset.Kind, asset.Value, err)
		}
	}
	if rejected > 0 {
		s.note(fmt.Sprintf("%s rejected %d unsafe record(s)", source, rejected))
	}
	return nil
}

// note records a coverage gap for the operator, without affecting Exit.
func (s *OSINTStage) note(message string) {
	s.reporter = append(s.reporter, message)
	if s.deps.OnSource != nil {
		s.deps.OnSource(message, nil)
	}
}

// targetHosts returns the distinct hostnames the stage may query.
func (s *OSINTStage) targetHosts() []string {
	seen := make(map[string]bool, len(s.deps.Targets))
	hosts := make([]string, 0, len(s.deps.Targets))

	for _, target := range s.deps.Targets {
		host := hostFromTarget(target)
		if host == "" || seen[host] {
			continue
		}
		if err := osint.ValidateTarget(host); err != nil {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// hostFromTarget extracts the hostname a target designates.
func hostFromTarget(target domain.Target) string {
	value := strings.TrimSpace(target.Value)
	if value == "" {
		return ""
	}
	switch target.Kind {
	case domain.TargetDomain, domain.TargetHost:
		return strings.ToLower(value)
	case domain.TargetIP:
		return value
	case domain.TargetURL:
		parsed, err := url.Parse(value)
		if err != nil {
			return ""
		}
		return strings.ToLower(parsed.Hostname())
	case domain.TargetCIDR, domain.TargetRepo, domain.TargetBinary:
		// These kinds name a network, a repository, or a file rather than a
		// single hostname, so there is nothing to query.
		return ""
	}
	return ""
}
