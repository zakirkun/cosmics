# Cosmics — Agent Guide

Terminal-native, LLM-driven penetration-testing platform in Go. Six-stage pipeline: Recon → OSINT → Analysis → Mapper → Exploit → Reporting.

## Commands

```
go build ./...                              # compile everything
go vet ./...                                # vet
go test -race -shuffle=on -count=1 ./...    # full suite
go test ./internal/store/ -run Migrate      # one package, filtered
golangci-lint run ./...                     # static analysis
```

## Architecture

| Path | Owns |
|---|---|
| `cmd/cosmics/` | Entry point. Flags, wiring, `app.Run`. Under 50 LOC. |
| `internal/domain/` | Pure types, smart constructors, state machines. **No imports outside stdlib.** |
| `internal/config/` | `config.yaml` + env loading, validation, secret redaction. Stores env var *names*, never values. |
| `internal/store/` | SQLite. Embedded migrations, two-handle pool, repositories. |
| `internal/tui/` | bubbletea v2 shell. **Imports `internal/domain` only.** No SQL, no I/O. |
| `internal/llm/` | Provider abstraction, streaming, tool calls, fallback router. *(M2)* |
| `internal/exec/` | Docker executor, streaming, artifacts. *(M3)* |
| `internal/pipeline/` | Stage orchestration, exit predicates. *(M3)* |

Dependency direction is strictly downward: `tui` → `app` → `pipeline`/`agent` → `llm`/`tools`/`exec`/`memory` → `store` → `domain`.

## Non-negotiable conventions

- `context.Context` is the first parameter of every function that does I/O.
- Errors wrapped with `%w`, checked with `errors.Is`/`errors.As`. No `_ = err`.
- Typed sentinel errors in each package. No bare string comparison on errors.
- `internal/domain` never imports anything but the standard library.
- Domain types are constructed only through `New*` functions that validate.
- Value receivers unless mutation is the point.
- No `any`/`interface{}` in domain or store signatures.
- No `panic` outside `main` and tests.
- 250 pure LOC per file limit. Split by responsibility.
- Time and randomness are injected, never called directly in domain code.

## Pinned dependencies — do not "upgrade"

| Module | Version | Why pinned |
|---|---|---|
| `modernc.org/sqlite` | **v1.59.0** | v1.60+ requires Go 1.26. **Build breaks.** |
| `charm.land/bubbletea/v2` | **v2.0.9** | v2.0.10+ requires Go 1.26. **Build breaks.** |
| `charm.land/bubbles/v2` | v2.2.1 | Matches bubbletea v2.0.9. |
| `charm.land/lipgloss/v2` | v2.0.6 | Matches. |

Local toolchain is `go1.25.13` with `GOTOOLCHAIN=local`. A `go get -u` silently breaks the build.

Charm v2 modules live at `charm.land/...`. The `github.com/charmbracelet/...` v2 paths produce a module path mismatch and will not resolve.

## Database rules

- Driver name is `"sqlite"` (modernc), not `"sqlite3"`.
- Two handles: writer `MaxOpenConns(1)`, reader `MaxOpenConns(4)`.
- **Never write inside a `rows.Next()` loop on the writer handle** — it deadlocks.
- Migrations execute one file as a **single `Exec`**. Do not split on `;`: `CREATE TRIGGER ... BEGIN ... END;` contains internal semicolons.
- `events`, `llm_calls`, `audit_trail` are append-only, enforced by triggers.
- `engagements.authorized_by`/`authorized_at` are immutable, enforced by a trigger.

## Determinism

- No `time.Now()` in domain code — inject `domain.Clock`.
- No `time.Sleep` in tests — subscribe to signals instead.
- Tests use `t.TempDir()` and never share state across packages.

## Authoritative specs

`docs/CORE.md` is normative. Where a doc and code disagree, one is a bug. Read `docs/ERD.md` before touching the schema and `docs/ARCHITECTURE.md` before adding a package.
