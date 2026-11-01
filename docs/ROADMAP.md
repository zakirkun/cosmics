# Cosmics — Roadmap

**Version:** 1.0.0
**Status:** Active
**Current milestone:** M0

---

## Guiding Constraints

1. **Every milestone ends with something runnable.** No milestone produces only scaffolding.
2. **The pipeline is the product.** A stage that cannot run end-to-end is not "half done" — it is not done.
3. **Safety controls ship before offensive capability.** Audit + approvals land in M1/M2, before the first exploit tool in M3.
4. **Documentation moves with code.** A milestone is not closed until `PROGRESS.md` and the affected docs match reality.

---

## Milestone Overview

| Milestone | Theme | Outcome | Depends on |
|---|---|---|---|
| **M0** | Documentation & decisions | This doc set, approved, consistent | — |
| **M1** | Skeleton & persistence | Binary starts, DB migrates, config loads, transcript renders | M0 |
| **M2** | LLM layer & agent loop | Real streaming tool-calling loop against 9Router, reasoning visible | M1 |
| **M3** | Execution & recon | Docker executor + Kali image + full Recon stage producing assets | M2 |
| **M4** | Analysis, Mapper, OSINT | Three reasoning stages with deterministic exit criteria | M3 |
| **M5** | Exploitation & reporting | Gated exploitation, sessions, report generation | M4 |
| **v1.0** | Hardening | All release criteria met | M5 |

---

## M0 — Documentation & Decisions

**Goal:** A contributor can read the docs and know exactly what to build.

### Deliverables
- [x] `PRD.md` — requirements, decisions register, risks
- [x] `ERD.md` — full schema, DDL, indices, invariants
- [x] `ARCHITECTURE.md` — layering, packages, provider facts, executor, concurrency
- [x] `FLOWCHART.md` — six stage flows, error paths, state machines
- [x] `CORE.md` — normative agent loop, prompts, memory, skills, contracts
- [x] `ROADMAP.md` — this document
- [x] `PROGRESS.md` — living tracker
- [ ] Design review pass: cross-document consistency check

### Exit Criteria
- Every decision in PRD §6 is reflected in ARCHITECTURE and CORE.
- Every table in ERD appears in a repository interface.
- Every stage in FLOWCHART has a matching exit predicate in CORE §3.2.
- No contradictions between documents (verified manually and by an automated doc-link check in M1).

---

## M1 — Skeleton & Persistence

**Goal:** `cosmics` launches a TUI, opens a database, applies migrations, shows an empty engagement list, and can create/resume an engagement with an authorization record.

### Deliverables
- [ ] `cmd/cosmics/main.go` — flags, version, `--debug`, `--data-dir`
- [ ] `internal/domain` — all types and enums from CORE §2
- [ ] `internal/config` — YAML load, env expansion, validation, `Redact()`
- [ ] `internal/store` — connection strategy, `Tx` helper, migrations runner with checksum
- [ ] `internal/store/migrations/0001_init.sql` — full schema from ERD
- [ ] Repositories: `EngagementRepo`, `TargetRepo`, `EventRepo`, `AuditRepo`
- [ ] `internal/audit` — hash-chain writer + `Verify()`
- [ ] `internal/tui` — Root model, header, sidebar, key help, engagement list, create form
- [ ] `internal/app` — composition root, lifecycle, graceful shutdown
- [ ] `cosmics doctor` — DB, config, Docker, gateway checks
- [ ] Embedded ID lists (WSTG, ATT&CK) as JSON

### Exit Criteria
- `cosmics` starts, renders, and exits cleanly on Windows PowerShell, Linux, macOS.
- `cosmics doctor` correctly reports a healthy 9Router gateway and a running Docker daemon, and gives actionable messages when either is down.
- Creating an engagement writes an `engagements` row plus an `audit_trail` entry whose hash chain verifies.
- Migration applies to an empty DB; a checksum mismatch aborts with a clear error.
- Kill -9 during startup leaves a DB that opens cleanly on the next run.
- All append-only triggers fire correctly (tests assert INSERT succeeds, UPDATE/DELETE aborts).

### Risks
- `modernc.org/sqlite` FTS5 availability — verified by a migration smoke test early.
- Windows path handling in `data_dir` — `filepath` everywhere, no string concatenation.

---

## M2 — LLM Layer & Agent Loop

**Goal:** A real conversation with `astral-projection` streams into the TUI with reasoning captured separately, and a tool call is round-tripped against a fake tool.

### Deliverables
- [ ] `internal/llm` — `Provider`, `Request`, `Response`, `Chunk`, `Message`, normalization
- [ ] `internal/llm/openai` — streaming client, SSE parser (comment lines, `[DONE]`, `reasoning_content`, incremental `tool_calls`)
- [ ] `internal/llm/anthropic` — native Messages client with thinking blocks and `tool_use`
- [ ] `internal/llm` router — per-stage chains, fallback, retry policy, cost accounting
- [ ] `internal/llm/pricing.yaml` + loader
- [ ] `internal/prompts` — embedded templates, `System()`, `StageContract()`, untrusted wrapper
- [ ] `internal/agent` — bounded loop, action dispatch, tool truncation, consecutive-error cap
- [ ] `internal/memory` — fact upsert with supersession, keyword retriever (FTS5), briefing synthesizer
- [ ] `internal/skills` — loader, front matter, chunker, indexer
- [ ] TUI: transcript pane with streaming, reasoning pane (collapsible), prompt input, session picker
- [ ] `llm_calls` persistence with `model_requested` vs `model_served`
- [ ] Fixture tests replaying captured gateway SSE

### Exit Criteria
- Streaming text appears incrementally; reasoning text appears in a separate pane; both are persisted.
- A fake `echo` tool round-trips: model calls it, result is returned, model continues, `finish_stage` is refused with a concrete missing-criteria message.
- Forcing the primary model to fail (bad model id) advances to the fallback entry and logs `fallback_index=1`.
- `429` retries with backoff honoring `Retry-After`; `400 Invalid model format` does **not** retry.
- Token and cost totals match provider-reported usage within 1%.
- Compaction triggers at 75% of window, produces a fixed-order summary, marks messages `pruned=1`, and preserves them in the DB.
- Tool-call argument accumulation across split SSE frames is correct (fixture with args split over 5 frames).
- Anthropic path streams `thinking_delta` and maps `stop_reason=tool_use` to `FinishTools`.

### Risks
- Tool-call delta edge cases across providers — mitigated by captured fixtures, not hand-written JSON.
- Provider timeout during long reasoning — streaming heartbeat + no fixed total timeout on streaming calls, only an idle timeout.

---

## M3 — Execution & Recon

**Goal:** The Recon stage runs real Dockerized tools against a lab target and produces a populated asset/service/fact graph.

### Deliverables
- [ ] `Dockerfile` + `scripts/build-tools-image.sh` — Kali base with the v1 toolset
- [ ] `internal/exec` — CLI-based executor, streaming, timeout+kill, artifact capture, hashing, reaping, `Ping`
- [ ] `internal/tools` — registry loader (embedded YAML), JSON Schema validation, argv templating, parser registry
- [ ] Tool parsers: `nmap_xml`, `json`, `jsonl`, `lines`, `none`
- [ ] Registry entries: `nmap`, `masscan`, `dnsx`, `httpx`, `subfinder`, `whatweb`, `tlsx`, `naabu`
- [ ] `internal/pipeline` — stage framework, `stage_runs` lifecycle, exit-check machinery
- [ ] Recon stage: passive-first orchestration, wildcard DNS detection, connect-vs-SYN decision, parallel host scan with semaphore
- [ ] Asset/service/fact extraction and dedup
- [ ] TUI: activity stream with tool output, per-stage progress, tool run inspector
- [ ] `cosmics tools list|doctor|pull`
- [ ] Images: pull-on-demand with progress; structured error when unavailable

### Exit Criteria
- `docker` down → Recon refuses to start with a fix-it message; no partial garbage rows.
- Against a lab target, Recon produces assets, services, and `web.tech` facts, with each fact traceable to a `tool_run`.
- A tool that exits 127 yields a structured `ToolUnavailable`, not a crash, and the model is told to try something else.
- Timeout kills the container (verified by `docker ps` showing nothing after) and persists `status=timeout`.
- Artifacts are content-addressed; identical output from two runs dedups to one blob.
- Re-running Recon is idempotent: no duplicate assets, `last_seen_at` updated.
- Orphan reaper removes containers from a simulated crash.
- Output flood (10 MB) does not block the TUI; truncation marker and artifact reference appear in context.

### Risks
- Raw-packet fidelity under Docker Desktop — registry caveats + connect-scan default + explicit `fidelity: reduced` marker in context.
- Image size / build time — multi-stage build, minimal package set, cache mounts.

---

## M4 — Analysis, Mapper, OSINT

**Goal:** Three additional stages run end-to-end with no LLM able to declare completion incorrectly.

### Deliverables
- [ ] OSINT stage + tools: `crtsh`, `dns_intel`, `rdap`, `wayback`, `github_search`, plus optional Shodan/Censys/VT adapters that no-op cleanly without keys
- [ ] Analysis stage: hypothesis generation, ranking, test-plan attachment, skill retrieval, `analysis.speculation` for low confidence
- [ ] Mapper stage: node/edge construction, edge validation against facts, Dijkstra ranking, ATT&CK tagging
- [ ] Memory: hybrid retriever (`keyword` default, `hybrid` opt-in), reciprocal rank fusion, stage-affinity boosting
- [ ] Fact supersession UI (memory inspector)
- [ ] Findings browser with severity filters, evidence drill-down
- [ ] Default skills: all 8 categories from CORE §8.4 with real content
- [ ] `cosmics skills validate|list`

### Exit Criteria
- A stage whose `Exit` predicate fails ends `failed` with a human-readable reason naming the missing item; it never reports success.
- Analysis creates findings **only** with a test plan; a finding without one is rejected by a constraint test.
- Mapper discards edges unsupported by facts (test with a deliberately hallucinated edge).
- Mapper's ranking is deterministic across runs on the same DB (golden test).
- OSINT runs entirely without paid API keys and records which sources were unavailable.
- OSINT never sends traffic to the target host (verified by an egress assertion in tests).
- Skill retrieval picks a relevant skill for a given tech stack and skips irrelevant large ones within budget.
- Fact supersession: writing a newer fact marks the old one `superseded_by` and retrieval returns only the new one.

### Risks
- LLM-invented topology — mitigated by edge validation (code, not prompt).
- Skill token cost — retrieval budget enforcement with a hard cap, verified by test.

---

## M5 — Exploitation & Reporting

**Goal:** Gated exploitation with real sessions, and a report that can be regenerated byte-identically.

### Deliverables
- [ ] Exploit stage: path selection, attempt caps, approval gate wiring, PoC evidence capture, verification predicate
- [ ] Post-exploitation: `access_sessions`, pivot chaining, `post_exploit_actions`, ATT&CK mapping, cleanup action
- [ ] Credential vault: AEAD encryption, keyring/passphrase KDF, `credential_id` resolution inside the executor
- [ ] Exploit/post tooling: `nuclei`, `ffuf`, `sqlmap`, `hydra`, `nikto`, `impacket`, `linpeas`/`winpeas` transfer
- [ ] Interactive containers for access sessions (long-lived `docker run -i` + exec)
- [ ] Approval modal: exact argv, reason, expiry, deny-by-timeout
- [ ] `internal/report` — deterministic queries, CVSS v4.0 scorer, Markdown + HTML renderers, templates
- [ ] Report narrative generation (prose only, ids cited)
- [ ] `cosmics report generate|regenerate`, `cosmics audit verify`, `cosmics gc`
- [ ] Sensitive-artifact exclusion and credential redaction in reports

### Exit Criteria
- A `destructive` tool cannot execute without a persisted `approved` row; denying or timing out yields `tool_runs.status='rejected'` with no container started.
- A finding becomes `verified=1` only when a `poc` evidence row exists and its producing run exited 0 with a matching success signal. A model claiming success without evidence leaves `verified=0`.
- Post-exploitation is refused in `exploit` mode and allowed in `full`.
- Credential plaintext appears in **zero** rows of `messages`, `llm_calls`, `events`, or rendered reports (assertion test over a full fixture run).
- Report generation refuses to run if any `verified=1` finding lacks evidence.
- Report regenerated 10× is byte-identical.
- Severity counts in the report equal a fresh `GROUP BY` query.
- An invalid CVSS vector is dropped, not silently mis-scored.
- `cosmics audit verify` detects a tampered row (test mutates a row directly and expects failure).

### Risks
- Approval fatigue leading operators to blindly approve — mitigate with exact argv display, reason, and a per-path attempt cap that reduces repeated prompts.
- Interactive container lifecycle leaks — reaper covers them; a session with no activity for N minutes is closed and recorded.

---

## v1.0 — Hardening

**Goal:** Meet every release criterion in PRD §11.

### Work
- [ ] Prompt-injection corpus test suite (≥ 20 payloads) in CI
- [ ] Fuzz tests: SSE parser, front matter parser, argv templater, CVSS parser
- [ ] Crash-recovery matrix: kill at each of 12 points in a stage, assert clean resume
- [ ] Performance: 10k asset engagement stays responsive; DB under 200 MB
- [ ] Cross-platform CI: Windows, Linux, macOS build + test
- [ ] `golangci-lint` clean, `go vet` clean, race detector clean
- [ ] User docs: `README.md`, `docs/USAGE.md`, `docs/TOOLS.md`, `docs/SKILLS.md`, `docs/SECURITY.md`
- [ ] Acceptable-use policy and authorization template
- [ ] Release artifacts + checksums

### Exit Criteria
PRD §11 satisfied in full.

---

## Deferred (post-v1)

| Item | Rationale for deferral |
|---|---|
| Headless mode + JSON output | Explicitly excluded by D6; straightforward to add behind the same engine |
| Local REST/WS API | Larger security surface; needs auth design |
| Web dashboard | Different product surface |
| PDF reports | HTML → PDF needs a renderer dependency or headless browser |
| Embedding-based retrieval | Gateway exposes no embedding models today; the `hybrid` retrieval path exists and is blocked only on a provider. Keyword mode is the default. |
| `vec0` index over `sqlite-vec` | Available without cgo, but measured ingest (~15.5 s per 20k vectors) loses to a warm in-Go scan (~18 ms). Revisit only if a persisted index becomes necessary. |
| Team collaboration / multi-tenant | Out of scope by NG2 |
| Cloud sync | Out of scope by NG7 |
| Plugin system for third-party tools | Registry covers the need; a plugin ABI is premature |
| Mobile app targets | Needs a different toolchain (apktool/frida); architecture allows it |
| Cloud provider assessment (AWS/Azure/GCP) | Additional tool images and credential model |

---

## Critical Path

```
M0 docs
  └─▶ M1 store+config+tui shell
        └─▶ M2 llm+agent+memory  ◀── highest technical risk, fix first
              └─▶ M3 exec+recon  ◀── highest integration risk
                    ├─▶ M4 osint  ─┐
                    ├─▶ M4 analysis┤ (parallelizable)
                    └─▶ M4 mapper ─┘
                          └─▶ M5 exploit+report
                                └─▶ v1.0 hardening
```

**M2 is the riskiest milestone.** If provider streaming, reasoning capture, and tool-call accumulation do not work reliably, no downstream stage matters. Prototype and fixture-capture before writing stage code.

**M3 is the second risk.** Docker on Windows with raw-packet tools has real limitations that only appear at runtime. Build the executor and probe fidelity early, with a lab target, before wiring stages.

---

## Estimation (solo developer, part-time)

| Milestone | Optimistic | Likely | Pessimistic |
|---|---|---|---|
| M0 | — | done | — |
| M1 | 1 wk | 2 wk | 3 wk |
| M2 | 2 wk | 3 wk | 5 wk |
| M3 | 2 wk | 4 wk | 6 wk |
| M4 | 2 wk | 4 wk | 6 wk |
| M5 | 3 wk | 5 wk | 8 wk |
| v1.0 | 1 wk | 2 wk | 4 wk |
| **Total** | **11 wk** | **20 wk** | **32 wk** |

Estimates assume the tool registry is filled incrementally and skills are written during M3–M5, not all at once.
