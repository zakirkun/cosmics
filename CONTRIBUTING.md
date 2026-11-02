# Contributing to Cosmics

## Before you write code

Read, in this order:

1. `docs/CORE.md` — the normative specification. Where a doc and the code
   disagree, one of them is a bug; say which you think it is.
2. `docs/ARCHITECTURE.md` — layering and the dependency direction. It is
   enforced in review: `internal/domain` imports nothing but the standard
   library, and `internal/tui` imports `internal/domain` only.
3. `AGENTS.md` — the conventions this repository actually holds itself to.

## Ground rules

These are enforced, not suggested. A pull request that breaks one will be asked
to change before review continues.

- **250 pure LOC per file.** Comments and blank lines do not count. Split by
  responsibility rather than by size.
- **`context.Context` is the first parameter** of every function that does I/O.
- **Errors are wrapped with `%w`** and inspected with `errors.Is` / `errors.As`.
  No `_ = err`. Typed sentinels live in the package that raises them.
- **No `time.Now()` in domain code.** Inject a clock. No `time.Sleep` in tests;
  subscribe to a signal instead.
- **No `any` in domain or store signatures.** The wire layer may use it while
  decoding; nothing below that may.
- **No `panic` outside `main` and tests.**
- **Tests use `t.TempDir()`** and never share state across packages.

## Determinism

Any function that produces ordered output must have an explicit ordering. Map
iteration is never an ordering. This matters most in the report renderer, the
mapper's graph construction, and severity sorting: two runs over the same
database must produce byte-identical output, and a test asserts it.

## Pinned dependencies

Do not upgrade these. Each was chosen because a newer version breaks the build
under the pinned Go toolchain:

| Module | Version | Why pinned |
|---|---|---|
| `modernc.org/sqlite` | v1.59.0 | v1.60+ requires Go 1.26 |
| `charm.land/bubbletea/v2` | v2.0.9 | v2.0.10+ requires Go 1.26 |

The Charm v2 modules live at `charm.land/...`. The `github.com/charmbracelet/...`
v2 paths produce a module path mismatch and will not resolve.

## The security invariants

A change that weakens any of these is a defect, regardless of how convenient it
is:

- A destructive tool does not run without a persisted `approved` row.
- A finding becomes `verified` only with proof-of-concept evidence and a
  producing run that exited zero. A model's claim never sets it.
- Tool arguments are schema-validated before an argv exists. argv is passed to
  the executor as a slice; no shell is ever involved.
- A credential is never written in plaintext, never logged, and never reaches
  LLM context or a rendered report.
- `events`, `llm_calls` and `audit_trail` are append-only. A trigger rejects an
  update or delete.

## Running the checks

```sh
go build ./...
go vet ./...
gofmt -l .            # must print nothing
gofumpt -l .          # must print nothing
golangci-lint run
go test -race -shuffle=on -count=1 ./...
```

The Docker executor suite needs a daemon and a built image:

```sh
docker build -t cosmics/kali-tools:latest -f docker/kali-tools.Dockerfile docker
go test -tags=docker ./internal/exec/
```

## Commits

Write the message for the reader who will `git blame` this line in a year. State
the defect being fixed and why the chosen fix is the right one. If you removed
something, say what made it dead. Do not narrate your process.

## Reporting a security issue

Do not open a public issue. See `SECURITY.md`.
