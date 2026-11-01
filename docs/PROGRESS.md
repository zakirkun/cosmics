# Cosmics — Progress

**Updated:** 2026-11-01
**Current milestone:** M3 complete, M4 not started
**Overall:** M0–M3 implemented and verified. Full suite green under `-race -shuffle=on`; Docker integration verified against the real daemon. M4 (Analysis/Mapper/OSINT stages) remains.

> This is a living document. Update it in the same commit as the work it describes.
> Status legend: `[ ]` not started · `[~]` in progress · `[x]` done · `[!]` blocked

---

## Snapshot

| Area | Status | Notes |
|---|---|---|
| Documentation set | `[x]` complete | 9 documents + README, cross-referenced |
| Decisions register | `[x]` locked | 7 decisions (D1–D7), all reflected in ARCHITECTURE and PRD |
| Gateway integration | `[x]` verified | Live probes against `http://localhost:20128/v1` |
| Data model | `[x]` shipped | 27 tables + 3 FTS5 indices, 17 triggers applied and tested |
| Dependency resolution | `[x]` verified | Every pin compiled and tested against `go1.25.13` |
| Code | `[~]` in progress | M1 packages complete; LLM layer, executor, and pipeline not started |
| Tool images | `[ ]` none | Kali image not built |
| Skills content | `[ ]` none | Directory exists, empty |

---

## Environment (verified)

| Item | Value | Verified |
|---|---|---|
| Go | `go1.25.13 windows/amd64` | 2026-11-01 |
| Docker | `29.9.2` (CLI reachable) | 2026-11-01 |
| `modernc.org/sqlite` | **must pin `v1.59.0`** — `v1.60.0+` requires Go ≥ 1.26 | 2026-11-01 |
| SQLite engine | `3.53.4` via `modernc.org/sqlite v1.59.0` | 2026-11-01 |
| FTS5 | **available** (porter unicode61 tokenizer, `bm25()` + `highlight()` verified) | 2026-11-01 |
| sqlite-vec | **available without cgo** via `modernc.org/sqlite/vec` — sqlite-vec **v0.1.9**, `vec0` KNN verified | 2026-11-01 |
| Write throughput | 2400 batched inserts = **15 ms**; 50 autocommit inserts = **194 ms** | 2026-11-01 |
| Migration DDL | whole-file `Exec` preserves `CREATE TRIGGER ... BEGIN ... END;` bodies; append-only + FTS sync triggers fire | 2026-11-01 |
| bubbletea | `charm.land/bubbletea/v2` **v2.0.9** — v2.0.10+ needs Go ≥ 1.26 | 2026-11-01 |
| bubbles | `charm.land/bubbles/v2` **v2.2.1** | 2026-11-01 |
| lipgloss | `charm.land/lipgloss/v2` **v2.0.6** | 2026-11-01 |
| glamour | `charm.land/glamour/v2` **v2.0.1** (no `WithAutoStyle`) | 2026-11-01 |
| huh | `charm.land/huh/v2` v2.0.3 | 2026-11-01 |
| OS | Windows 11 / PowerShell 5.1 | 2026-11-01 |
| Module | `github.com/cosmics/cosmics` | 2026-11-01 |
| sqlite3 CLI | present (`C:\msys64\mingw64\bin`) | 2026-11-01 |
| VCS | git initialized | 2026-11-01 |

---

## Live Gateway Findings

**Endpoint:** `http://localhost:20128/v1`
**Default model:** `astral-projection` (combo)

These were established by direct probing. They are recorded here because they are facts about the environment, not design intentions.

| Probe | Result | Impact |
|---|---|---|
| `GET /api/health` | `{"ok":true}` | Doctor check works |
| `GET /v1/models` | 600+ entries; `astral-projection` and `NoMercy` are `owned_by:"combo"` | Model discovery works; never hardcode lists |
| `POST /v1/chat/completions` (non-stream) | `model: "deepseek-v4.1-flash"`, `content: "COSMICS_OK"` | **Combo resolution changes the served model** — `model_requested` must be persisted separately |
| `POST /v1/chat/completions` (stream) | SSE with `: keep-alive`, `data: {...}`, `data: [DONE]` | Parser must skip comments |
| `delta.reasoning_content` | Present, streams thinking tokens | Reasoning pane required |
| Tool calling | `finish_reason:"tool_calls"`; `function.arguments` is a **JSON string**; `id` like `call_0_f206...` | Must accumulate then unmarshal |
| `usage` detail | `prompt_tokens_details.cached_tokens`, `completion_tokens_details.reasoning_tokens` | Cost accuracy |
| `GET /v1/models/info?id=...` | **404** (documented but not implemented) | Do not depend on it |
| `POST /v1/messages` | Works; Anthropic SSE events | Native Anthropic client viable through gateway |
| `content_block_start {type:"thinking"}` | Present even for non-reasoning models | Handle `thinking_delta` |
| `message_delta.usage` | `{input_tokens, output_tokens, estimated:true}` | Mark cost as estimated |
| `GET /v1/models/embedding` | **0 models** | **No embeddings available** → keyword-only retrieval in v1 |
| `GET /v1/models/web` | `[{id:"search-combo",kind:"webSearch"}]` | Advertised |
| `POST /v1/search` | `No credentials for provider: tavily/exa/brave-search/serper/perplexity` | **Web search unusable without provider keys** |
| `POST /v1/search` provider list | `tavily`, `exa`, `brave-search`, `serper`, `jina-ai` (no search), `perplexity`, `searxng` (blocked) | Wire format known for future enablement |
| `POST /v1/web/fetch` | Requires `provider` or `model`; same credential gate | Same |
| Error shape | `{"error":{"message","type","code"}}` | Map `code` to retry policy |

**Two findings changed the architecture:**
1. **No embedding models on the gateway** → retrieval defaults to keyword (FTS5 `bm25`). The *storage* capability turned out to be available (`modernc.org/sqlite/vec`, no cgo), so `ARCHITECTURE.md §8` documents a `keyword` default with an opt-in `hybrid` mode for when an embedding source exists.
2. **No web-search credentials** → OSINT must use direct HTTP sources (crt.sh, RDAP, Wayback, GitHub), not the gateway. Finding #1 and #2 are why `PRD.md` D4 lists free sources as the default.

---

## M0 — Documentation & Decisions

| Task | Status | File |
|---|---|---|
| Project skeleton | `[x]` | `go.mod`, directories |
| PRD | `[x]` | `docs/PRD.md` |
| ERD | `[x]` | `docs/ERD.md` |
| Architecture | `[x]` | `docs/ARCHITECTURE.md` |
| Flowchart | `[x]` | `docs/FLOWCHART.md` |
| Core spec | `[x]` | `docs/CORE.md` |
| Tool registry & image strategy | `[x]` | `docs/TOOLS.md` |
| Prior art & rationale | `[x]` | `docs/RESEARCH.md` |
| Roadmap | `[x]` | `docs/ROADMAP.md` |
| Progress tracker | `[x]` | `docs/PROGRESS.md` |
| Project overview | `[x]` | `README.md` |
| Cross-document consistency review | `[x]` | see below |
| Independent design review | `[ ]` | Not started |

### Consistency checks

| Check | Status | Evidence |
|---|---|---|
| Every decision D1–D7 appears in ARCHITECTURE | `[x]` | D4 and D7 were missing on first pass; added to §1 |
| FLOWCHART exit criteria defer to CORE §3.2 as normative | `[x]` | Authority note added; the two tables are summaries of one rule |
| Every ERD table referenced by CORE/ARCHITECTURE/FLOWCHART exists | `[x]` | Scripted check: 0 unresolved table references |
| ERD table count accurate | `[x]` | 27 tables + 3 FTS5 indices (was stated as 27 total) |
| Version pins agree across all documents | `[x]` | `v1.59.0` / `v2.0.9`; no stale `v1.46.0` claims remain |
| Retrieval modes agree between §8.1 and the config sample | `[x]` | `keyword` \| `vector` \| `hybrid` in both |
| Provider facts in ARCHITECTURE match live probes | `[x]` | Every row traced to a recorded probe |
| CORE §9 normalization table matches observed wire formats | `[x]` | OpenAI and Anthropic both exercised against the gateway |
| All internal markdown links resolve | `[x]` | Scripted check: 0 broken links |
| All documents UTF-8 without BOM, zero mojibake | `[x]` | BOM breaks migration tooling, so this is functional, not cosmetic |
| No placeholder or TODO markers left | `[x]` | Only legitimate `{{.arg}}` template syntax matches |
| Roadmap milestones reference real deliverables | `[x]` | Each M-item names a package or artifact |
| No contradicting scope statements between PRD and ROADMAP | `[x]` | Headless/API/PDF all deferred in both |

---

## M1 — Skeleton & Persistence

**Status: complete.** Verified with `go build ./...`, `go vet ./...`, `gofmt -l`, and `go test -race -shuffle=on -count=1 ./...` (all green).

| Task | Status | Notes |
|---|---|---|
| `go.mod` + dependency selection | `[x]` | Charm + sqlite + yaml + ulid + testify; both Go-1.26 ceilings pinned with comments |
| `internal/domain` types | `[x]` | 8 files; 99.5% coverage |
| `internal/clock` | `[x]` | System + Fixed clocks; 100% coverage |
| `internal/version` | `[x]` | Link-time stamping; 100% coverage |
| `internal/ids` | `[x]` | Embedded WSTG + ATT&CK sets with format validation; 81% coverage |
| `internal/config` loader + `Redact()` | `[x]` | Full YAML + env, 16 validation rules, secret redaction; 83.8% coverage |
| `internal/store` connection strategy | `[x]` | writer=1, reader=4, WAL, `Tx` with SQLITE_BUSY backoff |
| Migration runner + checksum | `[x]` | Whole-file Exec; checksum mismatch and unknown-version both abort |
| `0001_init.sql` full schema | `[x]` | 27 tables, 3 FTS5 indices, 17 triggers, 19 indices |
| FTS5 availability smoke test | `[x]` | **VERIFIED** on `v1.59.0` (SQLite 3.53.4) |
| sqlite-vec availability smoke test | `[x]` | **VERIFIED** `modernc.org/sqlite/vec`, sqlite-vec v0.1.9, no cgo |
| Trigger-body DDL smoke test | `[x]` | **VERIFIED** whole-file Exec preserves `BEGIN...END;`; append-only blocks UPDATE/DELETE |
| Repos: engagement, target, event, audit | `[x]` | Compare-and-set status update; hash-chained audit with `Verify` |
| `internal/audit` hash chain + verify | `[x]` | Tamper and broken-link detection both proven by test |
| TUI shell (header/sidebar/help) | `[x]` | bubbletea v2.0.9; six screens, help + approval overlays; 81.5% coverage |
| Engagement list + create form | `[~]` | List renders; the create form screen is a placeholder |
| `internal/app` composition root | `[x]` | Store + TUI wiring, `program.Send` for first-frame non-blocking load |
| Graceful shutdown | `[x]` | Store closed on return; TUI owns the session |
| `cosmics doctor` | `[x]` | Data dir, config, Docker daemon, gateway health, secret presence |
| Embedded WSTG/ATT&CK id lists | `[x]` | 109 ATT&CK techniques, 96 WSTG tests |
| Append-only trigger tests | `[x]` | `events`, `audit_trail`, `llm_calls`, `engagements.authorized_*` |
| Cross-platform smoke (win/linux/darwin) | `[ ]` | CI matrix configured; not yet exercised on real runners |

### Coverage at M1

| Package | Coverage |
|---|---|
| `internal/domain` | 99.5% |
| `internal/clock` | 100.0% |
| `internal/version` | 100.0% |
| `internal/tui` | 81.5% |
| `internal/config` | 83.8% |
| `internal/ids` | 81.0% |
| `internal/store` | 77.9% |
| `internal/cli` | 60.7% |
| `internal/doctor` | 56.4% |
| `internal/app` | 0.0% (thin composition root; exercised only by running) |

### M1 defects found and fixed

| Defect | Kind | Resolution |
|---|---|---|
| `ulid.NewMonotonicEntropy` does not exist in v2.1.2 | Compile | Use `ulid.Monotonic(rand.Reader, 0)`, which returns the entropy directly |
| Duplicate `"T1190 "` with a trailing space in `attack.json` | Data | Removed; test now rejects any identifier with surrounding whitespace |
| `AdvanceTo` stage expectation wrong for a completed engagement | Test | Expected value corrected to `EngageStageDone` |
| `Resume` test asserted the input was not `running` while its own table case was `running` | Test | Self-contradictory assertion removed |
| TUI helper-pane assertion looked for "Keyboard"; the overlay renders "Keys" | Test | Assertion aligned with real output |
| `internal/tui/table.go` imported lipgloss without using it | Compile | Import removed |
| Four domain test files and `view.go` exceeded the 250 pure-LOC ceiling | Structure | Split by responsibility into 8 files, all now under the ceiling |

---

## M2 — LLM Layer & Agent Loop

**Status: implemented and verified.** `go test -race -shuffle=on ./internal/llm/... ./internal/agent/ ./internal/memory/ ./internal/skills/` passes.

| Task | Status | Notes |
|---|---|---|
| `internal/llm` types + normalization | `[x]` | Provider-neutral Request/Response/Chunk; `Classify` retry policy |
| OpenAI-compatible streaming client | `[x]` | SSE, keep-alive skip, reasoning, per-index tool-arg accumulation; 75.6% |
| Anthropic Messages client | `[x]` | Multi-block merge (thinking+text+tool_use); 70.7% |
| Router: per-stage chains + fallback | `[x]` | Fails over only on retryable; stops on permanent; 85.6% |
| Retry policy (retryable vs not) | `[x]` | 400/401/403/404/422 permanent; 408/429/5xx retryable; Retry-After honoured |
| Pricing table + cost accounting | `[ ]` | Cost column exists; no per-model price table yet |
| `internal/prompts` templates | `[ ]` | **Not built** |
| `internal/agent` bounded loop | `[x]` | Iteration + consecutive-error caps, parallel tools, meta-actions; 66.0% |
| `internal/memory` facts + supersession | `[x]` | Confidence-aware supersede rule; 78.9% |
| Keyword retriever (FTS5 bm25) | `[x]` | Vector/hybrid return ErrEmbeddingUnavailable |
| Briefing synthesizer | `[x]` | Deterministic, sorted, truncation marker |
| `internal/skills` loader + chunker | `[x]` | 7 embedded skills, incremental sync, disables on removal; 82.1% |
| TUI transcript + reasoning pane | `[ ]` | **Not built** — TUI is still M1 shell |
| Streaming render throttle (~60ms) | `[ ]` | **Not built** |
| `llm_calls` persistence | `[x]` | `store.LLMCallRepo`; requested vs served both stored |
| Captured SSE fixtures | `[x]` | Reconstructed frame shapes in both client test suites |
| Compaction | `[ ]` | **Not built** |
| Injection corpus test | `[ ]` | **Not built** |

---

## M3 — Execution & Recon

**Status: execution layer verified against the real Docker daemon; stages not built.**

| Task | Status | Notes |
|---|---|---|
| Kali tool image Dockerfile | `[x]` | `docker/kali-tools.Dockerfile`; built, 995 MB |
| Image build script | `[x]` | Build command documented in the Dockerfile header |
| `internal/exec` CLI executor | `[x]` | argv array only, never a shell |
| Streaming demux into sink | `[x]` | Concurrent stdout/stderr, one sink |
| Timeout + container kill | `[x]` | `docker kill` by run label, then verified by test |
| Artifact capture + incremental SHA-256 | `[x]` | Per-stream digest + bounded buffer + dropped count |
| Orphan reaper | `[x]` | Label selector |
| `internal/tools` registry loader | `[x]` | Embedded YAML; `yamlTool` shim for schema re-encoding |
| JSON Schema arg validation | `[x]` | Stdlib subset; rejects metacharacter targets |
| argv templating | `[x]` | Every value quoted; unquoted template rejected at load |
| Parsers: nmap_xml, json, jsonl, lines, none | `[x]` | Named constants; **decoders not implemented** |
| Registry: nmap, masscan, dnsx, httpx, subfinder, whatweb, tlsx, naabu | `[~]` | **5 of 8 shipped** (nmap, httpx, subfinder, nuclei, ffuf); masscan/dnsx/whatweb/tlsx/naabu missing |
| Pipeline stage framework | `[x]` | Runner + Stage interface + exit predicates; 82.0% |
| Recon stage | `[ ]` | **Not built** |
| Wildcard DNS detection | `[ ]` | **Not built** |
| Connect-vs-SYN decision + caveat injection | `[~]` | Caveat shipped in nmap.yaml; decision logic absent |
| Asset/service/fact extraction | `[ ]` | **Not built** |
| TUI activity stream | `[ ]` | **Not built** |
| `cosmics tools list/doctor/pull` | `[ ]` | **Not built** |
| Fidelity probe on lab target | `[ ]` | **Not run** |

---

## M4 — Analysis, Mapper, OSINT

**Status: NOT STARTED.** Only the memory/skills substrate and the pipeline runner exist.

| Task | Status | Notes |
|---|---|---|
| OSINT stage | `[ ]` | |
| crt.sh adapter | `[ ]` | |
| DNS/RDAP/WHOIS adapter | `[ ]` | |
| Wayback CDX adapter | `[ ]` | |
| GitHub search adapter | `[ ]` | |
| Optional paid-source adapters | `[ ]` | |
| Analysis stage | `[ ]` | |
| Mapper stage | `[ ]` | |
| Hybrid retriever (RRF) | `[ ]` | Interface exists; fusion not implemented |
| Memory inspector UI | `[ ]` | |
| Findings browser | `[ ]` | |
| Default skills (8 categories) | `[~]` | 7 of 8 categories shipped; no `cloud`/`mobile` |
| `cosmics skills validate/list` | `[ ]` | |
| Determinism golden tests | `[ ]` | |

---

## M5 — Exploitation & Reporting

| Task | Status | Notes |
|---|---|---|
| Approval gate + modal | `[ ]` | Exact argv, expiry |
| Exploit stage | `[ ]` | Attempt caps, PoC evidence |
| Verification predicate | `[ ]` | Mechanical, not LLM |
| `access_sessions` + pivots | `[ ]` | |
| Post-exploitation actions | `[ ]` | ATT&CK mapped |
| Credential vault (AEAD) | `[ ]` | Never in context |
| Interactive containers | `[ ]` | Long-lived sessions |
| Exploit tools: nuclei, ffuf, sqlmap, hydra, nikto, impacket | `[ ]` | |
| Report: deterministic queries | `[ ]` | |
| CVSS v4.0 scorer | `[ ]` | Pure Go |
| Markdown + HTML renderers | `[ ]` | |
| Narrative generation (prose only) | `[ ]` | Ids cited |
| `cosmics report generate/regenerate` | `[ ]` | |
| `cosmics audit verify` | `[ ]` | |
| `cosmics gc` | `[ ]` | |
| Sensitive-artifact exclusion | `[ ]` | |
| Credential redaction assertions | `[ ]` | |

---

## v1.0 — Hardening

| Task | Status |
|---|---|
| Injection corpus ≥ 20 payloads | `[ ]` |
| Fuzz: SSE, front matter, argv, CVSS | `[ ]` |
| Crash-recovery matrix (12 kill points) | `[ ]` |
| Performance: 10k assets responsive | `[ ]` |
| Cross-platform CI | `[ ]` |
| `golangci-lint` + race clean | `[ ]` |
| User docs (README, USAGE, TOOLS, SKILLS, SECURITY) | `[ ]` |
| Acceptable-use policy | `[ ]` |
| Release artifacts + checksums | `[ ]` |

---

## Known Issues & Risks

| # | Issue | Severity | Status |
|---|---|---|---|
| I1 | Docker Desktop cannot provide reliable raw-packet scanning; SYN/ARP fidelity reduced | Medium | Accepted; caveats injected into context |
| I2 | No embedding models on the gateway → keyword-only retrieval by default | Medium | Accepted; `hybrid` mode implemented, blocked only on an embedding source |
| I3 | No web-search credentials → OSINT uses direct HTTP only | Low | Accepted; documented in D4 |
| I4 | `/v1/models/info` documented but 404 → cannot fetch per-model context windows | Medium | Must configure windows manually in `config.yaml` |
| I5 | Combo fallback means served model may differ from requested at any time | Medium | Persist both; cost table keyed by served model |
| I6 | Free-from targeting with no hard scope gate (D1) | High | Mitigated by authorization record + audit + approvals, not prevented |
| I7 | Full post-exploitation (D2) raises blast radius | High | Approval gate + attempt caps + audit |
| I8 | `estimated: true` on Anthropic usage → cost figures are approximations | Low | Flagged in report |

---

## Next Actions

1. Write `go.mod` with the pinned versions from the Environment table and a comment on each pin explaining the Go 1.26 ceiling.
2. Write `internal/store/migrations/0001_init.sql` from ERD §5 and prove every trigger fires (the pattern is already verified).
3. Build the TUI shell and prove clean startup/shutdown on Windows PowerShell.
4. Capture live SSE fixtures into `internal/llm/testdata/` for M2 replay tests.
5. Implement the two-handle DB (writer=1 / reader=4) and the `Tx` helper with `SQLITE_BUSY` backoff.

---

## Changelog

| Date | Change |
|---|---|
| 2026-11-01 | Documentation set created (PRD, ERD, ARCHITECTURE, FLOWCHART, CORE, ROADMAP, PROGRESS). Gateway integration verified by live probes. |
| 2026-11-01 | Added TOOLS.md and RESEARCH.md. Empirically verified: dependency version ceilings (Go 1.26 boundary), sqlite-vec without cgo, trigger-body DDL handling, batch-write throughput, Kali base contains zero tools. |
