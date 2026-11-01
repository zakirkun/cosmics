# Cosmics — Project Overview

**A terminal-native, LLM-driven penetration-testing platform in Go.**

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

# 2. Build the tool image (required — the Kali base ships no tools)
./scripts/build-images.ps1

# 3. Configure the gateway
$env:NINEROUTER_URL = "http://localhost:20128/v1"
$env:NINEROUTER_KEY = "sk-..."

# 4. Verify everything
./cosmics.exe doctor

# 5. Run
./cosmics.exe
```

---

## Status

**M0 — documentation complete. Implementation not started.**

| Phase | State |
|---|---|
| Documentation & decisions | ✅ complete |
| Skeleton & persistence | ⬜ not started |
| LLM layer & agent loop | ⬜ not started |
| Execution & recon | ⬜ not started |
| Analysis / Mapper / OSINT | ⬜ not started |
| Exploitation & reporting | ⬜ not started |

See [PROGRESS.md](PROGRESS.md) for the current state.

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
