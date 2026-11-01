# Cosmics — Prior Art & Design Rationale

**Version:** 1.0.0
**Status:** Reference. Explains *why* Cosmics is shaped the way it is.
**Sources:** project source code, published papers, and official documentation, verified 2026-11-01.

This document records what has already been built in this space, what worked, what demonstrably did not, and which specific design decisions in Cosmics are a response to each finding. It exists so that future contributors do not re-litigate settled questions, and so that the reasoning survives beyond any one person.

---

## 1. Why study prior art

Autonomous pentesting agents are not new. Between 2023 and 2026 at least six architectures were published with working code, and several were benchmarked. The failure modes are consistent and well documented. Building Cosmics without reading that record would mean rediscovering each failure expensively, on live targets.

The headline finding: **the bottleneck is not model quality, it is architecture.** AutoPenBench measured autonomous success at roughly **21%** versus **64% assisted** — a gap that persisted across model upgrades. The agents that failed did not fail because the model was too weak; they failed because the harness let the model wander, lose context, or hallucinate findings that were never evidenced.

---

## 2. Prior art survey

### 2.1 PentestGPT (USENIX Security '24)

**Architecture.** The original design decomposed work into three cooperating sessions:

| Session | Responsibility |
|---|---|
| **Reasoning session** | Maintains and updates a *Pentesting Task Tree* (PTT) — the structured plan of subtasks, statuses, and attributes |
| **Generation session** | Converts the current PTT leaf node into a concrete command |
| **Parsing session** | Converts raw tool output back into a summary that updates the PTT |

**Why it was decomposed.** The paper documents the failure it was designed to fix: a single monolithic conversation suffers **context loss** over a long engagement, and models **overemphasize recent tasks**, dropping early findings. Splitting reasoning from generation from parsing keeps each context narrow and puts the authoritative state (the PTT) outside the conversation.

**The v1.0 rewrite (2026)** is more instructive than the original. It moved to an **event-sourced SQLite store**, with:

- `RunSnapshot` — point-in-time state
- append-only `observations`, `attempts`, `transitions`
- **task leases** (an agent claims a subtask with an expiry, preventing two workers doing the same thing)
- **evidence sequences** (ordered evidence per claim)
- a **supervisor / executor** split, where the supervisor approves commands before execution

That is, the authors independently arrived at: persistent event log + explicit leases + a gate in front of execution. Those are the same three ideas Cosmics uses, which is meaningful corroboration.

**What Cosmics takes.** The PTT idea becomes `attack_nodes`/`attack_edges` (explicit graph) plus `stage_runs.plan_json`. The event-sourced memory becomes `events` + `facts` with supersession instead of in-place mutation. Supervisor/executor becomes the approval gate.

### 2.2 hackingBuddyGPT (ipa-lab)

**Architecture.** Deliberately minimal: a **simple agent loop** — observe, ask the model for one command, execute, feed output back. The authors argue explicitly that this simplicity is a feature, and that elaborate multi-agent orchestration frequently underperforms a tight loop with good context.

**The two mechanisms worth copying.**

1. **Ground-truth verification.** Their privilege-escalation agent does not trust the model's claim of success. It runs `check_command_success` and `is_root_from_id` against the actual shell state. A model claiming "I am root" is only believed if `id` says so.

2. **Hierarchical limits.** `Limits` objects compose as parent/child with `sub_limits`, so a sub-task inherits and narrows the parent's budget rather than resetting it.

**They also document two context strategies**: `HistoryFull` (everything) versus `HistoryCmdOnly` (commands only, dropping verbose output). The trade-off is real — dropping output saves tokens but loses the detail that often matters for detection.

**What Cosmics takes.** Verification becomes the `Exploit` stage exit predicate: `verified=1` requires a PoC evidence row from a successful run with a matching success signal, never the model's opinion (`CORE.md §3.2`, `FLOWCHART.md §8`). Limits become a hierarchical `Budget` that narrows per stage and per loop iteration.

**What Cosmics adds.** The simple loop works for a single objective ("get root on this box"). It does not work for a six-phase engagement where each phase has different tools, different success criteria, and a different cost profile. Cosmics keeps the tight inner loop and wraps it in deterministic stages.

### 2.3 CAI (Cybersecurity AI, aliasrobotics)

**Architecture.** A general agent framework with tooling, MCP support, and multi-agent orchestration. **Archived August 2026** — itself a data point about maintenance cost for broad frameworks.

**Notable contribution: prompt-injection research.** A dedicated paper studies attacking and defending AI hacking agents, and reports a **four-layer defense** approach. The threat is concrete: an agent reads a hostile HTTP response, a banner, or a file, and the content contains instructions. Because the agent legitimately follows instructions from its input stream, this is a direct path to arbitrary action.

**What Cosmics takes.** Defence in depth rather than one mechanism (`ARCHITECTURE.md §5.2`):

1. **Instruction hierarchy** in the system prompt — untrusted blocks are data, never instructions.
2. **Delimiting** with an escaped `<untrusted>` envelope so content cannot break out.
3. **Stage tool allow-list** — the decisive layer, because it is code, not a prompt. A hostile response during Reporting cannot cause an `nmap` call: the tool is not in the set.
4. **Approval gate** — destructive actions require a human keypress with the exact argv visible.

**What Cosmics does differently.** Layer 3 is the one that actually holds under adversarial pressure, because no amount of clever prompt text can invoke a tool that the harness refuses to expose. Cosmics makes the allow-list the primary control and treats prompting as defence in depth rather than the control itself.

### 2.4 AutoPenBench

**Finding.** Autonomous success ~**21%**; human-assisted ~**64%**. The gap is attributed to planning and state-tracking failures, not to individual command quality. Agents can run commands; they struggle to maintain a coherent multi-step goal.

**What Cosmics takes.** Every stage has a **deterministic exit predicate** evaluated in SQL (`CORE.md §3.2`). The model cannot declare victory. If the criteria fail, the stage fails with a concrete reason. This directly targets the failure mode AutoPenBench measured — agents that believe they are done.

### 2.5 VulnBot and RapidPen

**VulnBot** uses a multi-agent collaborative structure with a task-graph planning layer. Its contribution is the explicit separation of planning artefacts from execution.

**RapidPen** reports a useful, unglamorous technique: a **success-case retrieval** layer that retrieves prior successful command formulations for similar situations. It is cheap and effective — essentially a procedural-memory cache.

**What Cosmics takes.** RapidPen's idea maps directly onto `skills` + `memory_chunks`: retrieved markdown playbooks with stage affinity. The retrieval is not decorative; it is the mechanism by which the agent gets better over engagements without fine-tuning.

### 2.6 XBOW

**Positioning.** Production autonomous pentesting with a strong emphasis on **proof**: a finding is only reported if there is a working exploit. Their public material stresses that the validator, not the model, decides what is real.

**What Cosmics takes.** This is the same principle as `verified=1` requiring PoC evidence, and as `FLOWCHART.md §9` refusing to generate a report when a verified finding lacks evidence. The report generator treats an unevidenced verified finding as a defect and stops.

### 2.7 CyberSleuth

**Finding worth acting on:** multi-agent **specialisation** helps sustained reasoning, but **simple orchestration outperforms nested hierarchical architectures**. Depth of agent nesting is not correlated with better results; it is correlated with lost context.

**What Cosmics takes.** Six flat stages, one level deep. No agent-of-agents. A stage may consult a skill or ask the operator, but does not spawn subordinate agents. This is a deliberate rejection of hierarchical designs that the literature shows do not pay off.

### 2.8 Summary comparison

| Project | Control structure | Memory | Verification | Cosmics relationship |
|---|---|---|---|---|
| PentestGPT | Session decomposition + PTT | event-sourced SQLite (v1) | manual | Adopts event sourcing + supervisor gate |
| hackingBuddyGPT | Single tight loop | conversation history w/ compression | **ground-truth probe** | Adopts verification-first; wraps loop in stages |
| CAI | Broad multi-agent framework | framework-provided | weak | Adopts four-layer injection defence; avoids framework weight |
| AutoPenBench | benchmark | n/a | n/a | Motivates deterministic exit predicates |
| VulnBot | multi-agent + task graph | task graph | partial | Confirms graph-as-state approach |
| RapidPen | loop + success-case RAG | retrieval of successes | partial | Motivates skill retrieval |
| XBOW | production pipeline | proprietary | **exploit-only findings** | Motivates evidence-required reporting |
| CyberSleuth | multi-agent specialisation | — | — | Motivates flat (non-nested) orchestration |

---

## 3. Methodology standards

### 3.1 PTES

Phases: **Pre-engagement Interactions → Intelligence Gathering → Threat Modeling → Vulnerability Analysis → Exploitation → Post Exploitation → Reporting.**

| PTES phase | Cosmics stage |
|---|---|
| Pre-engagement | Engagement creation + authorization record |
| Intelligence Gathering | **Recon** + **OSINT** |
| Threat Modeling | **Analysis** |
| Vulnerability Analysis | **Analysis** + **Mapper** |
| Exploitation | **Exploit** |
| Post Exploitation | **Exploit** (full mode) |
| Reporting | **Reporting** |

Cosmics splits two PTES phases and merges none. The split of Intelligence Gathering into Recon and OSINT is deliberate: they have different tool sets, different network egress (target vs third-party), and different failure modes. Combining them would make the OSINT no-target-traffic guarantee unenforceable (`FLOWCHART.md §5`).

### 3.2 OWASP WSTG — numbering is directly usable

WSTG uses stable identifiers of the form `WSTG-<CATEGORY>-<NN>`. Confirmed categories include:

| Code | Category |
|---|---|
| `WSTG-INFO` | Information Gathering |
| `WSTG-CONF` | Configuration and Deployment Management Testing |
| `WSTG-IDNT` | Identity Management Testing |
| `WSTG-ATHN` | Authentication Testing |
| `WSTG-ATHZ` | Authorization Testing |
| `WSTG-SESS` | Session Management Testing |
| `WSTG-INPV` | Input Validation Testing |
| `WSTG-ERRH` | Error Handling |
| `WSTG-CRYP` | Cryptography |
| `WSTG-BUSL` | Business Logic Testing |
| `WSTG-CLNT` | Client-side Testing |
| `WSTG-APIT` | API Testing |

Note `ATHN` / `ATHZ`, not `AUTHN` / `AUTHZ`. Getting this wrong makes the mapping worthless in a report.

**Decision.** `findings.wstg_id` is populated from an **embedded validated list**. The LLM proposes an id; a validator checks it against the embedded set and blanks invalid values (`FLOWCHART.md §6`). A wrong id is worse than no id, because it misleads the client.

### 3.3 MITRE ATT&CK

Structure: **Tactic** (why) → **Technique** (`T####`) → **Sub-technique** (`T####.###`) → **Procedure** (how, implementation-specific).

ATT&CK's own documentation warns against the common misuse of treating it as a checklist of tests rather than a description of adversary behaviour. It fits Cosmics at two points only:

1. **Exploit stage** — mapping a successful action to the technique it represents (e.g. T1190 Exploit Public-Facing Application).
2. **Post-exploitation** — mapping actions to techniques for the report narrative.

Mapping is stored as `findings.attack_technique_ids_json` and `post_exploit_actions.attack_technique_id`, validated against the embedded id set. It is **not** used to drive testing; WSTG does that.

### 3.4 CVSS — v4.0, with an important implementation caveat

**Status.** CVSS v4.0 is current (published November 2023); v3.1 is archived by FIRST. New work should use v4.0.

**Caveat that changes the design.** CVSS v4.0 scoring is **not** a closed-form formula. It uses a `MacroVector` approach with **lookup tables** defined in the specification. Implementing it means transcribing those tables faithfully, not deriving a score.

**Decision.** `internal/report/cvss` implements v4.0 with the specification's tables embedded verbatim, with a conformance test against the official calculator's published examples. The model may *propose* a vector; the parser validates it and the scorer computes the number. An invalid vector is dropped and the finding renders as `cvss: unset` — never a confidently wrong score.

---

## 4. Design patterns adopted

### 4.1 Agent loop shape: plan → bounded act → deterministic exit

Survey of options:

| Pattern | Fit |
|---|---|
| **ReAct** (interleaved thought/action/observation) | Good for the inner loop; poor at long-horizon goal tracking on its own |
| **Plan-and-execute** | Good structure, but a plan made before evidence arrives is often wrong |
| **State machine / graph** | Strong control, weak flexibility |
| **Nested hierarchical agents** | Documented to underperform simple orchestration (CyberSleuth) |

**Cosmics uses a hybrid:** a deterministic **state machine** of six stages (control), each running a **bounded ReAct loop** (flexibility), with an **LLM-authored plan** at stage entry that is persisted but explicitly revisable (adaptation), and a **SQL exit predicate** (termination).

```
State machine  →  plan (LLM, persisted)
                  └→ bounded ReAct loop (LLM + tools)
                       └→ exit predicate (SQL, authoritative)
```

The model never owns control flow. It owns *decisions within* a stage.

### 4.2 Context compression: the "context rot" problem

Evidence: long contexts degrade — retrieval accuracy drops, middle content is lost ("lost in the middle"), and agents forget early commitments. Prior art offers three strategies:

| Strategy | Trade-off |
|---|---|
| `HistoryFull` (hackingBuddyGPT) | Lossless, unbounded cost |
| `HistoryCmdOnly` (hackingBuddyGPT) | Cheap, loses the detail that matters |
| Summarize-and-prune | Cheap, lossy, risk of losing a critical finding |

**Cosmics' answer is architectural, not textual:** important state does not live in the conversation at all.

- Facts → `facts` table (queryable, supersedable)
- Findings → `findings` table (never in context as the source of truth)
- Evidence → `evidence` + artifacts (hash-referenced)
- Narrative → `events` (append-only)

The conversation is therefore **disposable**. Compaction can summarize aggressively because nothing load-bearing depends on the transcript surviving (`CORE.md §6.5`). Each stage re-injects a **deterministic briefing** synthesized from the database (`CORE.md §6.3`), not from history.

This is the single most important structural difference from a chat-based agent: **the database is the memory; the context window is a viewport.**

### 4.3 Evidence chain

Every finding must trace to:

```
finding → evidence → tool_run → (argv, image digest, exit code, output_sha256) → artifact (bytes)
```

Reproduction is a recorded command, not a story. Hash chaining in `audit_trail` makes retroactive edits detectable (`ERD.md §3.23`).

**False-positive suppression** has three mechanisms:

1. Verified findings require a PoC run with exit 0 **and** a parser-confirmed success signal.
2. Analysis-stage hypotheses are `confidence=possible`, never `confirmed`, and the report labels them "unvalidated".
3. A model claim of success without a matching evidence row leaves `verified=0`.

### 4.4 Memory partitioning

CoALA distinguishes **working**, **episodic**, **semantic**, and **procedural** memory. Cosmics maps this explicitly:

| CoALA class | Cosmics | Retrieval |
|---|---|---|
| Working | current context assembly | rebuilt per call |
| Episodic | `events` | recency + FTS |
| Semantic | `facts` | FTS + key lookup, supersession-filtered |
| Procedural | `skills` + `memory_chunks` | FTS + stage affinity + tag overlap |

The partitioning matters because the retrieval strategies differ. Semantic facts need exact-key lookup ("what do we know about host X"); procedural knowledge needs fuzzy topical matching ("which playbook applies to a WordPress site with a login form"). Using one store for both would serve neither well.

---

## 5. Failure modes designed against

| Failure | Evidence it is real | Cosmics control |
|---|---|---|
| **Hallucinated vulnerabilities** | AutoPenBench; XBOW's emphasis on exploit-only findings | Findings cite evidence; verified findings require PoC; report refuses without evidence |
| **Unbounded tool loops** | Common in ReAct implementations | Iteration cap (40 default), consecutive-error cap (5), per-path attempt cap (3) |
| **Cost blowup** | Documented pain point across projects | Hard token + currency budget with 80%/100% thresholds |
| **Context overflow / rot** | AutoPenBench; PentestGPT's context-loss rationale | Facts in DB; deterministic briefing; auditable compaction |
| **Destructive commands** | CAI injection research | Stage allow-list + approval gate with exact argv + attempt caps |
| **Prompt injection from scanned content** | CAI four-layer paper | Untrusted envelope + instruction hierarchy + **allow-list** + approval gate |
| **Unsafe defaults** | Repeated across tools | Docker isolation, cap minimization, no shell interpolation, connect-scan default |
| **Agent declares success falsely** | hackingBuddyGPT ground-truth verification | SQL exit predicates; model may only propose completion |
| **Serving-model drift** | Observed live on the gateway (combo → different model) | Persist `model_requested` and `model_served`; price by served model |
| **Lost early findings** | PentestGPT context loss | Facts supersede in SQL; briefing regenerated every call |

---

## 6. What Cosmics deliberately does differently

1. **SQL exit predicates.** No surveyed project makes stage completion a database query. This is Cosmics' strongest anti-hallucination control, because the model cannot argue with a `SELECT`.

2. **Deterministic briefing synthesis.** Most projects let the model summarize its own state, which compounds error. Cosmics generates the briefing in Go from the database, so the model's view of reality cannot drift from the recorded reality.

3. **Flat six-stage pipeline.** Explicitly rejects nested agents per CyberSleuth's finding.

4. **Report narrative restricted to prose.** The model writes description/impact/remediation text only. Counts, severities, dates, asset names, and evidence excerpts are rendered by code. This eliminates the entire class of "the report says two critical but the database says three" defects.

5. **Requested vs served model tracking.** A consequence of gateway combos that most clients ignore, and the reason cost attribution is trustworthy.

6. **Docker-only tooling.** Reproducibility (image digest recorded per run) and host isolation, at the cost of raw-packet fidelity on Docker Desktop — which is recorded in context rather than hidden.

---

## 7. Open questions inherited from prior art

| Question | Status |
|---|---|
| Does per-path attempt capping measurably improve outcomes, or just reduce cost? | Measure in M5 with lab runs |
| Is skill retrieval actually load-bearing, or is the model ignoring it? | Instrument retrieval→action correlation in M4 |
| How much does the deterministic briefing cost in tokens vs an LLM summary? | Measure; expected cheaper and more accurate |
| Should the approval gate allow "approve all of this class for 10 minutes"? | Deferred; approval fatigue is a real risk but blanket approval defeats the control |
| Is `confidence=possible` useful to a client, or noise? | Review with a real report in M5 |

---

## 8. Sources

**Primary source code examined**
- `GreyDGL/PentestGPT` — legacy session decomposition; v1.0 event-sourced memory, task leases, supervisor/executor
- `ipa-lab/hackingbuddyGPT` — agent loop, ground-truth verification, hierarchical limits, history compression modes
- `aliasrobotics/cai` — architecture, guardrails (repo archived August 2026)

**Papers and benchmarks**
- PentestGPT (USENIX Security '24) — PTT, session decomposition, context-loss rationale
- AutoPenBench — 21% autonomous vs 64% assisted
- CAI framework paper and the prompt-injection-against-AI-hackers paper — four-layer defence
- CoALA — working / episodic / semantic / procedural memory
- ReAct — interleaved reasoning and acting
- CyberSleuth — specialisation helps, nested hierarchy does not
- VulnBot — multi-agent task graph
- RapidPen — success-case retrieval

**Standards**
- PTES — phase structure
- OWASP WSTG — test identifiers and reporting structure
- MITRE ATT&CK — tactic/technique/sub-technique/procedure, and its documented misuse
- FIRST CVSS v4.0 — specification including the MacroVector lookup tables

**Live environment probes (this project)**
- 9Router gateway at `http://localhost:20128/v1` — model discovery, combo resolution, streaming SSE, tool-call format, reasoning deltas, Anthropic `/v1/messages` path, embeddings availability, web-search credential gate. Recorded in `PROGRESS.md`.

**Unverified**
- Public documentation for XBOW, Pentest Agent Suite, Nebula, and similar commercial tools is marketing-level; architectural claims about them are marked as impressions, not verified facts.
