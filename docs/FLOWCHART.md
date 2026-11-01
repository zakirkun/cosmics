# Cosmics — Flowchart & Control Flow

**Version:** 1.0.0
**Status:** Approved for implementation (M0)
**Applies to:** `internal/pipeline`, `internal/agent`, `internal/exec`, `internal/report`

---

## 1. Conventions

All diagrams are Mermaid. Three node shapes carry meaning:

| Shape | Meaning |
|---|---|
| `[Rectangle]` | Deterministic code — no LLM involved |
| `([Stadium])` | LLM-driven decision or generation |
| `{Diamond}` | Branch / gate |

Every LLM call in these flows is routed through the provider abstraction described in `CORE.md §4`. Every tool call goes through the Docker executor described in `ARCHITECTURE.md §6`.

---

## 2. Master Pipeline

```mermaid
flowchart TD
    Start([operator launches cosmics]) --> Boot[Boot: load config, open DB, migrate, reap orphan containers]
    Boot --> Doctor{docker OK? models OK?}
    Doctor -- no --> DoctorFail[Show actionable error in TUI] --> Settings[Settings view]
    Doctor -- yes --> Home[Engagement list]

    Home --> Choose{New or resume?}
    Choose -- new --> Create[Create engagement: name, targets, mode, model profile]
    Choose -- resume --> Resume[Load engagement state + last stage_run]

    Create --> Acknowledge{{Authorization acknowledgment}}
    Acknowledge -- declined --> Home
    Acknowledge -- accepted --> Seed[Persist targets, write audit_trail: engagement_created + authorization_recorded]
    Seed --> Ready
    Resume --> Ready

    Ready --> Dispatch{Dispatch by engagements.stage}

    Dispatch -- none --> S1
    Dispatch -- recon --> S1
    Dispatch -- osint --> S2
    Dispatch -- analysis --> S3
    Dispatch -- mapper --> S4
    Dispatch -- exploit --> S5
    Dispatch -- reporting --> S6
    Dispatch -- done --> Finish

    subgraph Stages
        S1[Stage 1: Recon]
        S2[Stage 2: OSINT]
        S3[Stage 3: Analysis]
        S4[Stage 4: Mapper]
        S5[Stage 5: Exploit]
        S6[Stage 6: Reporting]
    end

    S1 --> G1{Recon exit criteria met?}
    G1 -- yes --> S2
    G1 -- retry --> S1
    G1 -- abort --> Abort

    S2 --> G2{OSINT exit criteria met?}
    G2 -- yes --> S3
    G2 -- retry --> S2
    G2 -- abort --> Abort

    S3 --> G3{Analysis exit criteria met?}
    G3 -- yes --> S4
    G3 -- retry --> S3
    G3 -- abort --> Abort

    S4 --> G4{Mapper exit criteria met?}
    G4 -- yes --> ModeGate
    G4 -- retry --> S4
    G4 -- abort --> Abort

    ModeGate{engagement.mode allows exploit?}
    ModeGate -- no --> S6
    ModeGate -- yes --> S5

    S5 --> G5{Exploit exit criteria met?}
    G5 -- yes --> S6
    G5 -- retry --> S5
    G5 -- abort --> Abort

    S6 --> Finish([Engagement completed])
    Abort([Engagement aborted])
```

### 2.1 Mode → Stage Matrix

| Mode | Recon | OSINT | Analysis | Mapper | Exploit | Reporting |
|---|---|---|---|---|---|---|
| `recon` | ✓ | ✓ | – | – | – | ✓ (surface report) |
| `assess` | ✓ | ✓ | ✓ | ✓ | – | ✓ |
| `exploit` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `full` | ✓ | ✓ | ✓ | ✓ | ✓ (+ post-exploitation) | ✓ |

`exploit` mode validates vulnerabilities with PoCs; `full` additionally permits post-exploitation actions (privilege escalation, lateral movement, data access) subject to approval.

---

## 3. Stage Execution Shape (applies to all six)

Every stage is the same deterministic skeleton wrapping an LLM inner loop. This is the single most important architectural invariant: **stages are not free-form agents; they are bounded procedures with LLM assistance.**

```mermaid
flowchart TD
    Enter([Stage invoked]) --> MakeRun[Create stage_runs row: status=running]
    MakeRun --> Event1[Emit event: stage_start]
    Event1 --> BuildCtx[Assemble context: briefing + facts + skills + memory retrieval]

    BuildCtx --> Plan([LLM: produce stage plan])
    Plan --> PlanOK{Plan validates against schema?}
    PlanOK -- no --> PlanRetry{attempts < 2?}
    PlanRetry -- yes --> Plan
    PlanRetry -- no --> FailPlan[status=failed, error=invalid plan]

    PlanOK -- yes --> PersistPlan[Persist plan_json]

    PersistPlan --> Loop{iteration < max_iter?}
    Loop -- no --> BudgetExceeded[status=failed, error=iteration cap]
    Loop -- yes --> Decide([LLM: choose next action])

    Decide --> ActionKind{action type}

    ActionKind -- tool_call --> Gate

    Gate{requires_approval?}
    Gate -- yes --> Approval[Create approvals row pending; show modal in TUI]
    Approval --> ApprDecision{operator decision}
    ApprDecision -- denied --> Denied[Persist tool_run status=rejected] --> Observe
    ApprDecision -- timeout --> Expired[decision=expired] --> Denied
    ApprDecision -- approved --> Exec
    Gate -- no --> Exec

    Exec[Execute in Docker container] --> Stream[Stream stdout/stderr to TUI]
    Stream --> Persist[Persist tool_run + artifacts + output_sha256]
    Persist --> Parse[Parse structured output via tool parser]
    Parse --> Emit[Extract assets, services, facts]

    ActionKind -- set_fact --> Fact[Upsert fact with supersession] --> Observe
    ActionKind -- add_finding --> Find[Create finding + require evidence] --> Observe
    ActionKind -- load_skill --> Skill[Retrieve skill markdown into context] --> Observe
    ActionKind -- ask_operator --> Ask[Surface question in TUI, block on answer] --> Observe
    ActionKind -- finish_stage --> ExitCheck

    Observe([Feed result back to LLM context]) --> Compact{context over threshold?}
    Compact -- yes --> DoCompact[Summarize pruned messages, mark pruned=1] --> Loop
    Compact -- no --> Loop

    ExitCheck{stage exit criteria satisfied?}
    ExitCheck -- no --> Loop
    ExitCheck -- yes --> Done[status=succeeded, set summary + rollups]
    Done --> Event2[Emit event: stage_end]
    Event2 --> Next([Advance engagements.stage])

    FailPlan --> Fail([Stage failed])
    BudgetExceeded --> Fail
```

### 3.1 Exit Criteria per Stage

Deterministic predicates evaluated against the database. **The LLM cannot declare a stage complete; it can only propose completion, and the predicate decides.**

> **Authority:** `CORE.md §3.2` is normative. The table below is a readable summary of the same predicates; where the two ever disagree, CORE wins and this table is the bug.

| Stage | Exit predicate (all must hold) |
|---|---|
| **Recon** | Every seeded target has ≥ 1 `assets` row with `state='alive'`, **or** the stage recorded an explicit `unreachable` fact for it. Port scan completed for each alive host (or marked skipped with reason). |
| **OSINT** | ≥ 1 OSINT source consulted per root domain; `assets` expanded with at least the enumeration result (possibly empty, recorded as fact); no pending OSINT tool run. |
| **Analysis** | Every alive asset has at least one `facts` row for `web.tech` or `net.service_summary`; ≥ 1 hypothesis finding or an explicit `analysis.no_hypothesis` fact. |
| **Mapper** | `attack_nodes` non-empty; every `findings` row of severity ≥ medium is referenced by at least one `attack_edges` row or an explicit `unmapped` note; a ranked path to each declared goal exists or is proven absent. |
| **Exploit** | Every finding marked `verified=1` has a `poc` evidence row; every attempted exploit has a terminal `tool_runs` row; every opened `access_sessions` row is either active with a note or closed. |
| **Reporting** | `reports` row exists, artifact written, severity counts match a fresh query, and no `findings` row is `verified=1` without evidence. |

If a predicate cannot be satisfied, the stage ends `failed` with a structured reason, and the operator chooses retry / skip / abort. It never silently "succeeds".

---

## 4. Stage 1 — Recon

```mermaid
flowchart TD
    A([Recon start]) --> B[Load seeded targets from engagement_targets]
    B --> C{Known scope of work?}
    C -- domain --> D1
    C -- ip/cidr --> D2
    C -- url --> D3

    D1[Passive DNS + certificate transparency first] --> E1[Subdomain enumeration]
    E1 --> E2[DNS resolution + wildcard detection]
    E2 --> F[HTTP probe: status, title, tech, TLS]
    D2[Host discovery] --> F
    D3[Direct HTTP probe] --> F

    F --> G{Alive hosts}
    G -- none --> G1[Record fact: all targets unreachable] --> Z
    G -- some --> H[Port scan per host]

    H --> H1{Tool supports SYN under Docker?}
    H1 -- no --> H2[Fall back to connect scan + note fidelity loss]
    H1 -- yes --> H3[SYN scan]
    H2 --> I
    H3 --> I[Service + version detection]

    I --> J[TLS inspection: cert, SANs, expiry, weak ciphers]
    J --> K[Technology fingerprinting]
    K --> L[Persist assets, services, facts]
    L --> M[Emit events: tool_start/tool_end per run]
    M --> Z([Recon exit check])

    style H2 fill:#fff4e6
    style G1 fill:#ffe6e6
```

**Recon design notes**

| Concern | Decision |
|---|---|
| Passive before active | Always. Cheap, safe, and often yields the widest surface before any packet reaches the target. |
| Wildcard DNS | Detected once per root domain; without it, subdomain enumeration produces unbounded false positives. |
| SYN vs connect | Registry declares `NET_RAW` requirement. Docker Desktop's Linux VM cannot do host-level raw packets reliably; connect scan is the default and the fidelity loss is recorded as a `tool_defs.notes` caveat surfaced to the LLM. |
| Parallelism | Independent hosts scanned concurrently with a semaphore; a single host's tools run sequentially to avoid interfering results. |
| Idempotency | Re-running recon merges assets by `(kind, value)` and updates `last_seen_at`; it never duplicates. |

---

## 5. Stage 2 — OSINT

```mermaid
flowchart TD
    A([OSINT start]) --> B[Enumerate root domains from assets]
    B --> C[For each root: run passive sources in parallel]

    C --> S1[crt.sh certificate transparency]
    C --> S2[DNS: A, AAAA, MX, TXT, NS, SOA, CAA]
    C --> S3[RDAP / WHOIS registration]
    C --> S4[Wayback CDX historical URLs]
    C --> S5[GitHub code search for org/repo leaks]
    C --> S6{API keys configured?}

    S6 -- yes --> S7[Shodan / Censys / VirusTotal / SecurityTrails / Hunter]
    S6 -- no --> S8[Skip: record fact osint.paid_sources=unavailable]

    S1 --> M[Merge into assets]
    S2 --> M
    S3 --> M
    S4 --> M
    S5 --> M
    S7 --> M
    S8 --> M

    M --> N[Deduplicate by canonical value]
    N --> O[Attach provenance: source tool_run_id]
    O --> P[Candidate credential / leak triage]
    P --> Q{Any secret-shaped hit?}
    Q -- yes --> R[Record as finding category=data_exposure, confidence=possible]
    Q -- no --> T
    R --> T[Emit facts: osint.subdomain_count, osint.source_coverage]
    T --> Z([OSINT exit check])
```

**OSINT design notes**

| Concern | Decision |
|---|---|
| Source independence | Each source is a separate tool with its own timeout and failure isolation. One source failing never fails the stage. |
| Provenance | Every discovered asset keeps `source` = tool id, so a report can attribute "found by crt.sh" vs "found by bruteforce". |
| Rate limits | Per-source token bucket in the executor; free sources get conservative defaults. |
| Leaked secrets | Recorded as `possible` confidence only. Verification is Analysis's job, never OSINT's. |
| No-key degradation | Explicitly recorded as a fact so the LLM does not hallucinate coverage it did not have. |

---

## 6. Stage 3 — Analysis

This is where reasoning models earn their cost. It is also where hallucination risk concentrates, so every claim must bind to persisted evidence.

```mermaid
flowchart TD
    A([Analysis start]) --> B[Build briefing from facts + assets + services]
    B --> C[Retrieve relevant skills by stage affinity + tags]
    C --> D[Retrieve prior findings and lessons via hybrid search]

    D --> E([LLM: cluster surface into attack surface groups])
    E --> F[For each cluster: hypothesize weaknesses]

    F --> G{Hypothesis type}
    G -- tech-known --> G1[Map tech to known vuln classes → candidate checks]
    G -- config --> G2[Map to misconfiguration checks]
    G -- auth --> G3[Map to authentication weaknesses]
    G -- logic --> G4[Needs manual reasoning; mark requires_human]

    G1 --> H
    G2 --> H
    G3 --> H
    G4 --> H

    H([LLM: rank hypotheses by likelihood × impact ÷ cost])
    H --> I{Confidence threshold}
    I -- high --> J[Create finding: confidence=likely, verified=0]
    I -- medium --> K[Create finding: confidence=possible]
    I -- low --> L[Record as fact: analysis.speculation, do not create finding]

    J --> M[Attach test plan: which tool, which args, expected signal]
    K --> M
    M --> N[Tie every finding to asset_id + endpoint]
    N --> O[Write analysis summary + WSTG mapping]
    O --> Z([Analysis exit check])

    style L fill:#f0f0f0
```

**Analysis design notes**

| Concern | Decision |
|---|---|
| No finding without a test plan | A finding created in Analysis is a *hypothesis*; it must carry the tool + args that would confirm or refute it. This makes Exploit mechanical rather than creative. |
| Evidence binding | Analysis findings have no tool evidence yet by definition; `verified` stays 0 and the report labels them "unvalidated". The Reporting stage refuses to call an unvalidated finding "confirmed". |
| Low-confidence discipline | Speculation is stored as a fact, not a finding. This keeps the report clean and prevents severity inflation. |
| Skill injection | Skills are retrieved by category affinity, not dumped wholesale — a 5 000-token skill on Active Directory is useless against a WordPress site and wastes budget. |
| WSTG mapping | The LLM proposes a `wstg_id`; a validator checks it against the embedded WSTG id list and blanks invalid values. |

---

## 7. Stage 4 — Mapper

Builds and ranks the attack graph. Deterministic graph algorithms do the ranking; the LLM supplies semantics (what an edge means) and the narrative.

```mermaid
flowchart TD
    A([Mapper start]) --> B[Load assets, services, findings, credentials]
    B --> C[Create attack_nodes for each entity]
    C --> C1[asset nodes]
    C --> C2[service nodes]
    C --> C3[vuln nodes from findings]
    C --> C4[credential nodes]
    C --> C5[goal nodes from engagement config]

    C1 --> D
    C2 --> D
    C3 --> D
    C4 --> D
    C5 --> D([LLM: propose edges between nodes])

    D --> E[Validate each edge against known facts]
    E --> F{Edge supported by evidence?}
    F -- yes --> G[Persist edge with confidence]
    F -- partial --> H[Persist edge with low confidence + note]
    F -- no --> I[Discard]

    G --> J
    H --> J
    I --> J[Compute connectivity from entry nodes]

    J --> K[Dijkstra/BFS over edge cost × inverse confidence]
    K --> L[Rank paths to each goal node]
    L --> M{Mitigation nodes present?}
    M -- yes --> N[Mark path as blocked, reduce priority]
    M -- no --> O[Promote path priority]

    N --> P
    O --> P[Persist node priorities]
    P --> Q([LLM: name top N attack paths])
    Q --> R[Write mapping summary + ATT&CK technique ids]
    R --> Z([Mapper exit check])
```

**Mapper design notes**

| Concern | Decision |
|---|---|
| LLM proposes, code validates | An edge with no supporting fact is discarded. This is the primary defense against the LLM inventing a network topology. |
| Ranking is deterministic | Cost and confidence come from the DB; `Dijkstra` in Go computes paths. The LLM only labels and narrates. Reproducible across runs. |
| Goals come from config | Defaults: domain admin / root, data access, public data exposure. Overridable per engagement. |
| Cycle handling | Graph traversal is cycle-safe; `attack_edges` uniqueness prevents duplicate parallel edges, and traversal tracks visited nodes. |
| Empty graph is valid | If nothing is exploitable, the mapper records that conclusion explicitly. "No path found" is a real, reportable result. |

---

## 8. Stage 5 — Exploit

Highest risk stage. Every action passes through the approval gate unless explicitly marked `safe`.

```mermaid
flowchart TD
    A([Exploit start]) --> B{mode allows exploit?}
    B -- no --> Abort1([Skip stage, note in report])
    B -- yes --> C[Load ranked paths from Mapper]

    C --> D([LLM: choose next path + technique])
    D --> E[Resolve technique to tool + args]
    E --> F{Tool danger_level}

    F -- safe --> G[Execute]
    F -- intrusive --> H{auto-approve intrusive?}
    H -- yes --> G
    H -- no --> I[Approval modal]
    F -- destructive --> I

    I --> J{Operator decision}
    J -- denied --> K[Record rejection, mark path blocked] --> D
    J -- expired --> K
    J -- approved --> G

    G --> L[Stream output, persist tool_run + artifacts]
    L --> M{Exploit succeeded?}

    M -- no --> N[Record attempt + failure reason]
    N --> O{Attempts on this path < cap?}
    O -- yes --> D
    O -- no --> P[Mark path exhausted] --> D

    M -- yes --> Q[Capture proof: evidence row kind=poc]
    Q --> R[Set finding verified=1, confidence=confirmed]
    R --> S{Access obtained?}

    S -- no --> T[Record data/behavior proof only] --> D
    S -- yes --> U[Create access_sessions row]

    U --> V{full mode?}
    V -- no --> W[Close session, record note] --> D
    V -- yes --> X[Post-exploitation loop]

    X --> X1([LLM: choose post-exploit action])
    X1 --> X2{Action type}
    X2 -- enumerate --> X3
    X2 -- escalate --> X4
    X2 -- dump --> X5
    X2 -- pivot --> X6
    X2 -- persist --> X7
    X2 -- cleanup --> X8

    X3 --> Y[Approval gate + execute]
    X4 --> Y
    X5 --> Y
    X6 --> Y
    X7 --> Y
    X8 --> Y

    Y --> Y1[Persist post_exploit_actions + ATT&CK id]
    Y1 --> Y2{New hosts reachable?}
    Y2 -- yes --> Y3[Create assets + new session chain link] --> D
    Y2 -- no --> Y4{More actions worth taking?}
    Y4 -- yes --> X1
    Y4 -- no --> D

    style I fill:#ffe6e6
    style F fill:#fff4e6
```

**Exploit design notes**

| Concern | Decision |
|---|---|
| Approval is per-action, not per-stage | The operator approves the specific command, seeing exact argv. Blanket stage approval is not offered. |
| Attempt cap | Per-path attempt cap (default 3) prevents the agent hammering a dead end and burning budget. |
| Verification is mechanical | A finding becomes `confirmed` only when a `poc` evidence row exists whose `tool_run.exit_code` is 0 **and** whose parsed output matches the tool's success signal. The LLM's opinion is not sufficient. |
| Credentials never enter context | The LLM references a `credential_id`; the executor resolves it at container launch. The model never sees the secret, so it cannot leak it into a report. |
| Session chain | Pivots create child `access_sessions` rows, giving a real attack-path record for the report's narrative. |
| Cleanup is an action | `cleanup` is a first-class post-exploit action so artifacts dropped on target can be removed and recorded. |
| Failure is data | A failed exploit attempt is persisted with its reason. Repeated failure across paths is itself a finding ("resilient configuration"). |

---

## 9. Stage 6 — Reporting

```mermaid
flowchart TD
    A([Reporting start]) --> B[Snapshot: query all findings, evidence, assets, sessions]
    B --> C{Verified finding without evidence?}
    C -- yes --> C1[Refuse: mark finding unverified, log error]
    C -- no --> D
    C1 --> D[Compute severity counts from DB, not from LLM]

    D --> E([LLM: write executive summary from facts only])
    E --> F{Summary cites a finding id that exists?}
    F -- no --> E
    F -- yes --> G

    G([LLM: per-finding narrative: description, impact, remediation])
    G --> H[Render deterministic sections in code]

    H --> H1[Title, engagement metadata, authorization record]
    H --> H2[Executive summary from LLM]
    H --> H3[Scope and methodology]
    H --> H4[Findings table by severity]
    H --> H5[Per-finding detail + evidence appendix]
    H --> H6[Attack path narrative from Mapper]
    H --> H7[Sessions and post-exploitation timeline]
    H --> H8[Remediation priority list]
    H --> H9[Audit appendix: tool runs, token/cost, approval log]

    H1 --> I
    H2 --> I
    H3 --> I
    H4 --> I
    H5 --> I
    H6 --> I
    H7 --> I
    H8 --> I
    H9 --> I[Assemble Markdown]

    I --> J[HTML render from same structure]
    J --> K[Hash input set → db_snapshot_hash]
    K --> L[Write artifacts + reports row]
    L --> M[Emit event: report_generated + audit_trail]
    M --> Z([Reporting exit check])
```

**Reporting design notes**

| Concern | Decision |
|---|---|
| Counts are computed, never generated | Severity totals come from `SELECT`. An LLM-written summary claiming "two critical" when the DB says three is a defect, so the count is injected into the prompt as fixed text. |
| Deterministic ordering | Findings sorted by severity, then CVSS descending, then ULID. Same DB → same document. |
| Refusal to fabricate | A finding with `verified=1` and no evidence aborts report generation with a clear error rather than shipping an unprovable claim. |
| Sensitive data | `artifacts.sensitive=1` excluded unless `--include-sensitive`. Credentials render as `username / (redacted)` only. |
| Regeneration | `cosmics report regenerate <id>` re-runs the deterministic path; the LLM summary is cached in `reports` unless `--refresh-narrative`. |

---

## 10. Interaction Flows

### 10.1 Operator pauses mid-stage

```mermaid
sequenceDiagram
    participant U as Operator
    participant T as TUI
    participant P as Pipeline
    participant R as Running tool

    U->>T: press p (pause)
    T->>P: cancel(ctx) with reason=paused
    P->>R: kill container, persist tool_run status=cancelled
    P->>P: engagements.status=paused
    P->>P: stage_runs.status=aborted, error="operator pause"
    P-->>T: render "paused at stage X"
    Note over P: Facts and assets already written are kept
    U->>T: press r (resume)
    T->>P: resume from engagements.stage, new stage_run attempt+1
```

**Invariant:** pausing never discards partial results. Facts already extracted remain; the re-run merges rather than duplicating.

### 10.2 Destructive action approval

```mermaid
sequenceDiagram
    participant A as Agent
    participant G as Gate
    participant T as TUI
    participant U as Operator
    participant E as Executor

    A->>G: tool_call(destructive)
    G->>G: INSERT approvals decision=pending
    G->>T: push modal (command, reason, expiry)
    T->>U: render modal, focus locked
    U->>T: press y
    T->>G: Decide(approved, by=operator)
    G->>G: UPDATE approvals decision=approved
    G->>E: Execute(tool_run bound to approval_id)
    E-->>T: stream output
    Note over G: expiry default 300s → decision=expired
```

### 10.3 Crash recovery

```mermaid
flowchart TD
    A([Process starts]) --> B[Open DB, run pending migrations]
    B --> C[Query tool_runs WHERE status IN 'queued','running']
    C --> D[For each: mark status=failed, error=interrupted by restart]
    D --> E[Query stage_runs WHERE status='running']
    E --> F[Mark status=aborted, error=interrupted]
    F --> G[Docker: remove containers with label cosmics.run]
    G --> H[Query approvals decision=pending past expires_at]
    H --> I[Mark decision=expired]
    I --> J[Render resume prompt for engagements.status=running]
```

**Invariant:** startup reconciliation is unconditional and idempotent. A restart midway through a write must not leave a `running` row that blocks forever.

---

## 11. LLM Tool-Calling Loop (inner detail)

Every stage's decision step runs this loop.

```mermaid
flowchart TD
    A([Build call]) --> B[Assemble messages from session + stage briefing]
    B --> C[Attach tool schemas allowed for this stage]
    C --> D[Attach retrieved skills as system context]
    D --> E{Budget check}
    E -- over budget --> Z1([Hard stop: budget_exhausted])
    E -- ok --> F[Stream request to provider]

    F --> G{Response type}
    G -- text --> H[Append assistant message] --> End1
    G -- tool_use --> I[Append assistant message with tool_calls]
    I --> J[Execute tool call through gate + executor]
    J --> K[Append role=tool message with result]
    K --> L{iteration++ < max_iter}
    L -- yes --> A
    L -- no --> Z2([Stop: iteration cap])
    G -- error retryable --> M[Backoff + retry, respecting Retry-After]
    M --> N{retries < max}
    N -- yes --> F
    N -- no --> O[Try next model in stage fallback chain]
    O --> P{chain exhausted}
    P -- yes --> Z3([Stage failed])
    P -- no --> F

    End1([Return assistant text])
```

### 11.1 Stage Tool Allow-list

The LLM cannot call any tool it wants. Each stage exposes an explicit subset, enforced in code before execution.

| Stage | Allowed tool categories | Notes |
|---|---|---|
| Recon | `recon`, `network`, `web` (read) | No exploitation, no post-exploitation |
| OSINT | `osint` | Network egress to third parties only; no target scanning |
| Analysis | none (pure reasoning) + skill loading + fact writing | Analysis never touches the target |
| Mapper | none (pure reasoning) + graph writes | Deterministic graph algorithms supplied as functions |
| Exploit | `exploit`, `web`, `post` (if `full`) | Every `intrusive`/`destructive` call gated |
| Reporting | none + DB reads | Reporting never executes anything against a target |

**Rationale:** a stage-scoped allow-list converts a whole class of prompt-injection attacks into a no-op. A hostile HTTP response telling the agent to run `nmap` during Reporting has no such tool available.

---

## 12. Error Taxonomy & Handling

| Class | Example | Handling | Operator-visible |
|---|---|---|---|
| **Transient** | 429, 502, container pull timeout | Retry with exponential backoff + jitter, honor `Retry-After`, max 5 | Spinner, then "retrying (2/5)" |
| **Provider degrade** | Model 503 "all accounts unavailable" | Advance fallback chain, log `fallback_index` | Banner: "fallback to <model>" |
| **Tool failure** | nmap exits non-zero | Persist failure, feed stderr to LLM, let it choose an alternative | Activity stream shows red line |
| **Tool unavailable** | Image missing, binary absent | Structured error; registry suggests `cosmics tools install <id>` | Modal with fix command |
| **Plan invalid** | LLM returns malformed JSON | One repair attempt with the parse error appended, then fail the stage | Transcript shows the error |
| **Exit criteria unmet** | Recon found no live hosts | Stage `failed` with reason; operator chooses retry/skip/abort | Blocking choice prompt |
| **Budget** | 80% / 100% of token or USD budget | Warn at 80%, hard stop at 100% | Toast + status bar |
| **Approval timeout** | Operator away | `decision=expired`, tool_run `rejected`, agent told "denied by timeout" | Modal dismisses |
| **Fatal** | DB corrupt, schema mismatch | Abort process with exit code, preserve DB, print recovery steps | Full-screen error |
| **Cancellation** | Ctrl+C, pause | `context.Canceled` propagates to container kill and HTTP abort | Clean "cancelled" state |

---

## 13. Context Assembly (deterministic, inspectable)

Every LLM call is built from these blocks, in this order. The TUI can render the exact assembled prompt (`v` key in transcript).

```mermaid
flowchart LR
    A[1. System prompt: role, rules, safety hierarchy] --> B[2. Stage contract: goal, allowed tools, exit criteria]
    B --> C[3. Engagement briefing: synthesized facts summary]
    C --> D[4. Target facts: relevant subset by retrieval]
    D --> E[5. Retrieved skills: top-k by hybrid score]
    E --> F[6. Recent messages: unpruned tail]
    F --> G[7. Rolling compaction summary]
    G --> H[8. Current instruction / last tool result]
    H --> I[Assemble → token budget check → call]
```

| Block | Budget share | Source |
|---|---|---|
| System + contract | ~10% | Static templates in `internal/prompts` |
| Engagement briefing | ~15% | Deterministic synthesis from `facts` |
| Retrieved skills | ~20% | Hybrid retrieval over `skills` + `memory_chunks` |
| Recent messages | ~40% | `messages WHERE pruned=0 ORDER BY seq DESC LIMIT n` |
| Compaction summary | ~10% | `sessions.compaction_summary` |
| Current instruction | ~5% | Last tool result, truncated per policy |

**Truncation policy:** tool output over `tool_defs.max_output_bytes` is head/tail truncated with a marker `[truncated N bytes; artifact <id>]`. The full artifact stays on disk and can be re-read on demand via a `read_artifact` pseudo-tool.

---

## 14. State Machine Reference

### `engagements.status`

```mermaid
stateDiagram-v2
    [*] --> draft
    draft --> ready: authorization acknowledged
    ready --> running: start
    running --> paused: operator pause
    paused --> running: resume
    running --> completed: reporting done
    running --> failed: unrecoverable error
    running --> aborted: operator abort
    paused --> aborted: operator abort
    completed --> ready: reopen (audited)
    failed --> ready: retry (audited)
    aborted --> ready: reopen (audited)
    completed --> [*]
    aborted --> [*]
```

### `stage_runs.status`

```mermaid
stateDiagram-v2
    [*] --> pending
    pending --> running
    running --> succeeded: exit criteria met
    running --> failed: error or criteria unmet
    running --> aborted: cancel
    pending --> skipped: mode excludes stage
    failed --> running: retry (new attempt row)
```

### `tool_runs.status`

```mermaid
stateDiagram-v2
    [*] --> queued
    queued --> rejected: approval denied
    queued --> running: container started
    running --> succeeded: exit 0
    running --> failed: exit != 0
    running --> timeout: deadline exceeded
    running --> cancelled: operator cancel
```

---

## 15. Invariants Checklist

These must hold at every point; each is testable.

1. A `finding` with `verified=1` always has ≥ 1 `evidence` row whose `tool_run_id` points to a `tool_runs` row with a non-null `output_sha256`.
2. A `tool_runs` row is never mutated after `finished_at` is set (trigger-enforced).
3. `audit_trail` contains an entry for every target ever touched and every exploit launched.
4. Deleting an engagement leaves `audit_trail` rows intact.
5. No credential plaintext appears in `messages.content`, `llm_calls.request_json`, `events.message`, or any rendered report.
6. A stage cannot advance while any of its `tool_runs` is `queued` or `running`.
7. Report severity counts equal a fresh `GROUP BY severity` query on the same snapshot.
8. Restarting mid-write leaves zero `running` tool runs and zero `pending` approvals past expiry.
9. An LLM call cannot reference a tool outside the current stage's allow-list.
10. Total `usage_ledger` cost equals the sum of `llm_calls.cost_usd_micros` for the engagement.
