package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cosmics/cosmics/internal/domain"
	"github.com/cosmics/cosmics/internal/osint"
	"github.com/cosmics/cosmics/internal/pipeline"
	"github.com/cosmics/cosmics/internal/pipeline/stages"
	"github.com/cosmics/cosmics/internal/store"
)

// ErrPipelineIncomplete reports that a stage could not be assembled.
var ErrPipelineIncomplete = fmt.Errorf("app: pipeline cannot be assembled")

// StageSet is the assembled stage list plus the runner built over it.
type StageSet struct {
	Runner *pipeline.Runner
	Stages []pipeline.Stage
}

// BuildPipeline assembles the analysis stages and the runner for one database.
//
// Reporting is absent on purpose: it renders finished work rather than producing
// it, so it is a command over the data, not a stage that could mark an engagement
// complete by writing a document about itself.
func BuildPipeline(db *store.DB, clock domain.Clock) (StageSet, error) {
	if db == nil {
		return StageSet{}, fmt.Errorf("database: %w", ErrPipelineIncomplete)
	}
	if clock == nil {
		return StageSet{}, fmt.Errorf("clock: %w", ErrPipelineIncomplete)
	}

	assetStore := stages.NewAssetStore(store.NewAssetRepo(db))
	findingStore := stages.NewFindingStore(store.NewFindingRepo(db))
	attackStore := stages.NewAttackStore(store.NewAttackRepo(db))

	osintStage, err := stages.NewOSINTStage(stages.OSINTDeps{
		Assets:  assetStore,
		Sources: osint.NewRegistry(defaultOSINTSources()...),
		Clock:   clock,
	})
	if err != nil {
		return StageSet{}, fmt.Errorf("osint stage: %w", err)
	}

	analysisStage, err := stages.NewAnalysisStage(stages.AnalysisDeps{
		Assets:   assetStore,
		Services: assetStore,
		Findings: findingStore,
		Clock:    clock,
	})
	if err != nil {
		return StageSet{}, fmt.Errorf("analysis stage: %w", err)
	}

	mapperStage, err := stages.NewMapperStage(stages.MapperDeps{
		Assets:   assetStore,
		Findings: findingStore,
		Graph:    attackStore,
		Clock:    clock,
	})
	if err != nil {
		return StageSet{}, fmt.Errorf("mapper stage: %w", err)
	}

	listed := []pipeline.Stage{osintStage, analysisStage, mapperStage}

	runner, err := pipeline.NewRunner(pipeline.RunnerOptions{
		Stages: listed,
		Store:  newRunStore(db),
		Clock:  clock,
	})
	if err != nil {
		return StageSet{}, fmt.Errorf("runner: %w", err)
	}

	return StageSet{Runner: runner, Stages: listed}, nil
}

// defaultOSINTSources returns the adapters that need no credentials.
//
// Each talks to a public endpoint over HTTPS. A source needing a key is not
// registered at all rather than registered and failing per query, so an operator
// never sees a source that cannot work appear to be tried.
func defaultOSINTSources() []osint.Source {
	return []osint.Source{
		osint.NewCRTSh(httpClient()),
		osint.NewDNS(osint.NewNetResolver()),
		osint.NewWayback(httpClient()),
		osint.NewRDAP(httpClient()),
	}
}

// httpClient returns a client with a bounded total request time.
func httpClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// RunEngagement executes one engagement through the assembled pipeline.
//
// progress may be nil. It is called with the stage being entered, which is the
// only signal a caller needs to keep a dashboard honest about where a run is.
func RunEngagement(
	ctx context.Context,
	db *store.DB,
	clk domain.Clock,
	engagement domain.Engagement,
	progress func(domain.StageKind),
) (domain.Engagement, error) {
	set, err := BuildPipeline(db, clk)
	if err != nil {
		return domain.Engagement{}, fmt.Errorf("build pipeline: %w", err)
	}

	if progress != nil {
		set.Runner.SetProgressHook(progress)
	}

	finished, err := set.Runner.Run(ctx, engagement)
	if err != nil {
		return finished, fmt.Errorf("run engagement: %w", err)
	}
	return finished, nil
}
