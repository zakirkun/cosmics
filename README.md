# Cosmics

**A terminal-native, LLM-driven penetration-testing platform in Go.**

[![ci](https://github.com/zakirkun/cosmics/actions/workflows/ci.yml/badge.svg)](https://github.com/zakirkun/cosmics/actions/workflows/ci.yml)
[![licence: GPL-3.0-or-later](https://img.shields.io/badge/licence-GPL--3.0--or--later-blue.svg)](LICENSE)

Cosmics runs a deterministic six-stage engagement pipeline — **Recon → OSINT → Analysis → Mapper → Exploit → Reporting** — where an LLM plans and reasons, Docker/Kali containers execute, and SQLite records everything as evidence.

```
   ┌──────────────────────────────────────────────────────────────┐
   │                     Cosmics (single binary)                  │
   │                                                              │
   │   bubbletea TUI ──► pipeline ──► agent ──► LLM gateway       │
   │                        │           │                         │
   │                        │           └──► tools registry       │
   │                        │                    │                │
   │                        └──► SQLite ◄────────┴──► Docker/Kali │
   │                             (facts, evidence, audit)         │
   └──────────────────────────────────────────────────────────────┘
```

---

## Why

Existing options force a bad trade-off: manual scripts (not reproducible), fixed scanners (can't chain findings or reason), LLM chat (no execution, no evidence), cloud SaaS (opaque, exfiltrates data). Cosmics is deterministic stage control + real containerized tooling + LLM reasoning over evidence + durable memory + local data ownership, in a terminal.

---

## Documentation

Start here, in this order.

| Document | Read it for |
|---|---|
| **[PRD.md](PRD.md)** | Requirements, decisions register (D1–D7), risks, release criteria |
| **[ARCHITECTURE.md](ARCHITECTURE.md)** | Layering, packages, LLM boundary, Docker executor, concurrency, verified dependency versions |
| **[CORE.md](CORE.md)** | **Normative spec** — agent loop, prompts, memory, skills, provider contracts, tool contract |
| **[ERD.md](ERD.md)** | 27 tables + 3 FTS5 indices, DDL, invariants |
| **[FLOWCHART.md](FLOWCHART.md)** | Stage-by-stage control flow, error paths, state machines |
| **[TOOLS.md](TOOLS.md)** | Kali image build, tool registry, execution contract, fidelity policy |
| **[RESEARCH.md](RESEARCH.md)** | Prior art (PentestGPT, hackingBuddyGPT, CAI, XBOW…), methodology standards, why each decision was made |
| **[ROADMAP.md](ROADMAP.md)** | M0–M5 milestones with exit criteria |
| **[PROGRESS.md](PROGRESS.md)** | Living status, live-probed environment facts |

---

## Design in five sentences

1. **Stages are deterministic; the model only fills in decisions.** Six stages, each with an entry rule, a bounded ReAct loop, and an exit predicate evaluated in SQL. The model may *propose* completion; the database decides.
2. **The database is the memory, the context window is a viewport.** Facts, findings, and evidence live in SQLite, so compaction can be aggressive without losing anything load-bearing.
3. **Every offensive action is a container.** No host tooling. Every run records its image digest, exact argv, exit code, and an output hash.
4. **The model proposes, code disposes.** Tool arguments are schema-validated, argv is never shell-interpolated, findings need evidence, and destructive actions need a human keypress with the command shown.
5. **Two providers, one contract.** OpenAI-compatible (9Router default) and native Anthropic, with per-stage model chains and fallback.

---

## Quick start

```powershell
# 1. Build
go build -o cosmics.exe ./cmd/cosmics

# 2. Build the tool image (required - the Kali base ships no tools)
docker build -t cosmics/kali-tools:latest -f docker/kali-tools.Dockerfile docker

# 3. First run: the setup wizard discovers models from the gateway
./cosmics.exe setup

# 4. Verify everything
./cosmics.exe doctor

# 5. Run
./cosmics.exe
```

On a first run with no `~/.cosmics/config.yaml`, the wizard starts automatically.
It asks for the gateway endpoint, then calls `GET /v1/models` and presents a
searchable list of what the gateway actually serves. The API key is kept in
memory for discovery only: the file records the *name* of the environment
variable (`NINEROUTER_KEY`), never the key.

```powershell
$env:NINEROUTER_KEY = "sk-..."
```

---

## Status

**M0-M3 implemented and verified. M4 and M5 in progress.**

| Phase | State |
|---|---|
| Documentation and decisions | complete |
| Skeleton and persistence | complete, 78.7% statement coverage |
| LLM layer and agent loop | complete, 85.6% router coverage |
| Execution and recon tooling | complete, Docker suite passes against the real daemon |
| Analysis / Mapper / OSINT | implemented |
| Exploitation and reporting | in progress |

Every figure above is measured by `go test -cover`, not estimated. The full
suite runs green under `-race -shuffle=on` across 19 packages.

### What is verified, not assumed

- `go build`, `go vet`, `gofmt`, `gofumpt` and `golangci-lint` all clean.
- 490+ test functions, none of them skipped to make the suite pass.
- The Docker executor was exercised against a real daemon: streaming, stderr
  demultiplexing, timeout kill, non-zero exit, `--network none` isolation and a
  read-only root filesystem.
- `cosmics/kali-tools:latest` builds at 995 MB and every registry tool executes.

See [PROGRESS.md](PROGRESS.md) for per-task status, including what is *not* done.

---

## Verified environment facts

Established by direct probing, not assumption. Details in [PROGRESS.md](PROGRESS.md).

- **Gateway** `http://localhost:20128/v1` is live. `astral-projection` is a combo that resolves to `deepseek-v4.1-flash` — the requested and served model differ, so both are persisted.
- **Tool calling works**, including **4 parallel tool calls in one response**, and `reasoning_content` streams separately from `content`.
- **No embedding models** are exposed → retrieval defaults to keyword (SQLite FTS5). `sqlite-vec` **is** available without cgo, so `hybrid` mode works once an embedding source exists.
- **No web-search credentials** are configured → OSINT uses direct HTTP sources, not the gateway.
- **`kalilinux/kali-rolling:latest` is 121 MB and contains zero security tools.** The tool image must be built.
- **Pinned versions** (Go 1.25.13 cannot use newer): `modernc.org/sqlite v1.59.0`, `charm.land/bubbletea/v2 v2.0.9`.

---

## Security & Authorization

Cosmics performs **real exploitation, including post-exploitation**. It is for **authorized security testing only**.

- Every engagement requires a recorded **authorization acknowledgment** before stage 1 begins.
- Every target touched and every exploit launched is written to an **append-only, hash-chained audit trail** that survives engagement deletion.
- Destructive actions require **explicit per-action operator approval**, showing the exact command.
- Credentials are encrypted at rest and **never enter LLM context**.

There is **no hard scope gate** by design (decision D1): enforcement is documentary and auditable, not technical. You are responsible for having permission.

---

## Requirements

| Component | Requirement |
|---|---|
| Go | 1.25.x (1.26 not yet supported by the pinned deps) |
| Docker | Desktop with the WSL2 Linux engine, running |
| OS | Windows 11 (primary), Linux, macOS |
| LLM | 9Router gateway, or an Anthropic API key |

No cgo. No gcc. One binary.

---

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).

Cosmics performs real offensive security testing. Running it against systems you
do not own or have written permission to test is illegal in most jurisdictions
and is not a use this project supports.
