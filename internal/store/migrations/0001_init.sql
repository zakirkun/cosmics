-- 0001_init.sql - initial Cosmics schema.
--
-- Authoritative source: docs/ERD.md section 5 ("Full DDL"). Every table, index,
-- FTS5 virtual table and trigger from that section is declared here.
--
-- EXCLUSION: `schema_migrations` is deliberately NOT declared in this file. The
-- Go migrator (internal/store/migrate.go) creates it with CREATE TABLE IF NOT
-- EXISTS before applying any file, so declaring it here would fail with
-- "table schema_migrations already exists" on a fresh database.
--
-- EXECUTION CONTRACT: the migrator passes this entire file to a single Exec
-- call. It must never be split on ';' - a `CREATE TRIGGER ... BEGIN ... END;`
-- body contains internal semicolons and a naive splitter produces broken SQL.
--
-- Timestamps are INTEGER Unix epoch milliseconds (UTC). Booleans are INTEGER
-- 0/1 with a CHECK. Money is INTEGER micro-USD. Hashes are lowercase hex
-- SHA-256. All primary keys are TEXT ULIDs.

-- ---------------------------------------------------------------------------
-- Engagements: the root aggregate.
-- ---------------------------------------------------------------------------

CREATE TABLE engagements (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL UNIQUE,
    client              TEXT,
    mode                TEXT NOT NULL CHECK (mode IN ('recon','assess','exploit','full')),
    status              TEXT NOT NULL CHECK (status IN ('draft','ready','running','paused','completed','failed','aborted')),
    stage               TEXT NOT NULL CHECK (stage IN ('none','recon','osint','analysis','mapper','exploit','reporting','done')),
    model_profile       TEXT NOT NULL DEFAULT 'default',
    authorized_by       TEXT NOT NULL,
    authorization_note  TEXT,
    authorized_at       INTEGER NOT NULL,
    budget_usd_micros   INTEGER NOT NULL DEFAULT 0 CHECK (budget_usd_micros >= 0),
    spent_usd_micros    INTEGER NOT NULL DEFAULT 0,
    token_budget        INTEGER NOT NULL DEFAULT 0,
    tokens_used         INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL,
    started_at          INTEGER,
    finished_at         INTEGER,
    config_json         TEXT NOT NULL DEFAULT '{}',
    error               TEXT
);

-- ERD.md section 6: load the engagement dashboard without scanning.
CREATE INDEX idx_engagements_status ON engagements(status, updated_at);

CREATE TRIGGER trg_engagements_authorization_immutable
BEFORE UPDATE OF authorized_by, authorized_at ON engagements
WHEN OLD.authorized_by <> NEW.authorized_by OR OLD.authorized_at <> NEW.authorized_at
BEGIN
    SELECT RAISE(ABORT, 'authorization record is immutable');
END;

-- ---------------------------------------------------------------------------
-- Seed targets supplied by the operator. Derived assets are not stored here.
-- ---------------------------------------------------------------------------

CREATE TABLE engagement_targets (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('domain','host','ip','cidr','url','repo','binary')),
    value         TEXT NOT NULL,
    raw           TEXT,
    note          TEXT,
    created_at    INTEGER NOT NULL,
    UNIQUE (engagement_id, kind, value)
);

-- ---------------------------------------------------------------------------
-- Stage runs, sessions, messages, LLM calls.
-- ---------------------------------------------------------------------------

CREATE TABLE stage_runs (
    id                TEXT PRIMARY KEY,
    engagement_id     TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    stage             TEXT NOT NULL CHECK (stage IN ('recon','osint','analysis','mapper','exploit','reporting')),
    attempt           INTEGER NOT NULL CHECK (attempt >= 1),
    status            TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped','aborted')),
    session_id        TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    plan_json         TEXT,
    summary           TEXT,
    started_at        INTEGER,
    finished_at       INTEGER,
    tool_run_count    INTEGER NOT NULL DEFAULT 0,
    finding_count     INTEGER NOT NULL DEFAULT 0,
    fact_count        INTEGER NOT NULL DEFAULT 0,
    tokens_in         INTEGER NOT NULL DEFAULT 0,
    tokens_out        INTEGER NOT NULL DEFAULT 0,
    cost_usd_micros   INTEGER NOT NULL DEFAULT 0,
    iterations        INTEGER NOT NULL DEFAULT 0,
    error             TEXT,
    UNIQUE (engagement_id, stage, attempt)
);

CREATE TABLE sessions (
    id                  TEXT PRIMARY KEY,
    engagement_id       TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    stage_run_id        TEXT REFERENCES stage_runs(id) ON DELETE SET NULL,
    title               TEXT,
    model               TEXT NOT NULL,
    system_prompt_hash  TEXT NOT NULL,
    context_window      INTEGER,
    status              TEXT NOT NULL CHECK (status IN ('open','closed','compacted')),
    message_count       INTEGER NOT NULL DEFAULT 0,
    last_compaction_at  INTEGER,
    compaction_summary  TEXT,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE TABLE messages (
    id                  TEXT PRIMARY KEY,
    session_id          TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq                 INTEGER NOT NULL,
    role                TEXT NOT NULL CHECK (role IN ('system','user','assistant','tool')),
    content             TEXT,
    reasoning           TEXT,
    content_blocks_json TEXT,
    tool_calls_json     TEXT,
    tool_call_id        TEXT,
    token_in            INTEGER NOT NULL DEFAULT 0,
    token_out           INTEGER NOT NULL DEFAULT 0,
    pruned              INTEGER NOT NULL DEFAULT 0 CHECK (pruned IN (0,1)),
    created_at          INTEGER NOT NULL,
    UNIQUE (session_id, seq)
);

CREATE INDEX idx_messages_ctx ON messages(session_id, pruned, seq);

CREATE TABLE llm_calls (
    id                TEXT PRIMARY KEY,
    engagement_id     TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    session_id        TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    message_id        TEXT REFERENCES messages(id) ON DELETE SET NULL,
    stage             TEXT NOT NULL,
    provider          TEXT NOT NULL CHECK (provider IN ('openai_compatible','anthropic')),
    model_requested   TEXT NOT NULL,
    model_served      TEXT,
    fallback_index    INTEGER NOT NULL DEFAULT 0,
    purpose           TEXT NOT NULL CHECK (purpose IN ('stage_plan','tool_select','output_parse','summarize','report_write','compact')),
    request_json      TEXT NOT NULL,
    response_json     TEXT,
    status            TEXT NOT NULL CHECK (status IN ('ok','error','timeout','rate_limited','cancelled')),
    http_status       INTEGER,
    error_code        TEXT,
    error_message     TEXT,
    tokens_in         INTEGER NOT NULL DEFAULT 0,
    tokens_out        INTEGER NOT NULL DEFAULT 0,
    tokens_cached     INTEGER NOT NULL DEFAULT 0,
    cost_usd_micros   INTEGER NOT NULL DEFAULT 0,
    latency_ms        INTEGER,
    streamed          INTEGER NOT NULL DEFAULT 0 CHECK (streamed IN (0,1)),
    reasoning_tokens  INTEGER NOT NULL DEFAULT 0,
    started_at        INTEGER NOT NULL,
    finished_at       INTEGER NOT NULL
);

CREATE INDEX idx_llm_calls_eng_stage ON llm_calls(engagement_id, stage);
CREATE INDEX idx_llm_calls_status ON llm_calls(status);

CREATE TRIGGER trg_llm_calls_no_update
BEFORE UPDATE ON llm_calls
BEGIN
    SELECT RAISE(ABORT, 'llm_calls is append-only');
END;

CREATE TRIGGER trg_llm_calls_no_delete
BEFORE DELETE ON llm_calls
BEGIN
    SELECT RAISE(ABORT, 'llm_calls is append-only');
END;

-- ---------------------------------------------------------------------------
-- Tool registry and executions.
-- ---------------------------------------------------------------------------

CREATE TABLE tool_defs (
    id                TEXT PRIMARY KEY,
    category          TEXT NOT NULL CHECK (category IN ('recon','osint','web','network','exploit','post','report')),
    display_name      TEXT NOT NULL,
    image             TEXT NOT NULL,
    image_digest      TEXT,
    binary            TEXT NOT NULL,
    version_cmd       TEXT,
    arg_schema_json   TEXT NOT NULL,
    command_template  TEXT NOT NULL,
    capabilities_json TEXT,
    network_mode      TEXT NOT NULL DEFAULT 'bridge' CHECK (network_mode IN ('bridge','host','none')),
    default_timeout_s INTEGER NOT NULL DEFAULT 300 CHECK (default_timeout_s > 0),
    max_output_bytes  INTEGER NOT NULL DEFAULT 262144,
    danger_level      TEXT NOT NULL DEFAULT 'safe' CHECK (danger_level IN ('safe','intrusive','destructive')),
    requires_approval INTEGER NOT NULL DEFAULT 0 CHECK (requires_approval IN (0,1)),
    enabled           INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    notes             TEXT,
    updated_at        INTEGER NOT NULL
);

CREATE TABLE tool_runs (
    id                 TEXT PRIMARY KEY,
    engagement_id      TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    stage_run_id       TEXT REFERENCES stage_runs(id) ON DELETE SET NULL,
    llm_call_id        TEXT REFERENCES llm_calls(id) ON DELETE SET NULL,
    approval_id        TEXT REFERENCES approvals(id) ON DELETE SET NULL,
    tool_id            TEXT NOT NULL REFERENCES tool_defs(id),
    argv_json          TEXT NOT NULL,
    argv_display       TEXT NOT NULL,
    image              TEXT NOT NULL,
    image_digest       TEXT,
    container_id       TEXT,
    network_mode       TEXT NOT NULL,
    caps_json          TEXT,
    target             TEXT,
    status             TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','timeout','cancelled','rejected')),
    exit_code          INTEGER,
    stdout_artifact_id TEXT REFERENCES artifacts(id) ON DELETE SET NULL,
    stderr_artifact_id TEXT REFERENCES artifacts(id) ON DELETE SET NULL,
    output_preview     TEXT,
    output_sha256      TEXT,
    started_at         INTEGER NOT NULL,
    finished_at        INTEGER,
    duration_ms        INTEGER,
    parsed_json        TEXT,
    error              TEXT
);

CREATE INDEX idx_tool_runs_eng_tool ON tool_runs(engagement_id, tool_id);
CREATE INDEX idx_tool_runs_stage ON tool_runs(stage_run_id);
CREATE INDEX idx_tool_runs_status ON tool_runs(status);
CREATE INDEX idx_tool_runs_target ON tool_runs(target);

CREATE TRIGGER trg_tool_runs_no_update_after_finish
BEFORE UPDATE ON tool_runs
WHEN OLD.finished_at IS NOT NULL
 AND (NEW.argv_display <> OLD.argv_display OR NEW.output_sha256 IS NOT OLD.output_sha256 OR NEW.exit_code IS NOT OLD.exit_code)
BEGIN
    SELECT RAISE(ABORT, 'finished tool_runs are append-only');
END;

-- ---------------------------------------------------------------------------
-- Content-addressed artifacts and discovered surface.
-- ---------------------------------------------------------------------------

CREATE TABLE artifacts (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('tool_stdout','tool_stderr','http_capture','screenshot','dump','loot','report','pcap')),
    rel_path      TEXT NOT NULL,
    media_type    TEXT,
    byte_size     INTEGER NOT NULL,
    sha256        TEXT NOT NULL,
    encoding      TEXT NOT NULL DEFAULT 'utf-8' CHECK (encoding IN ('utf-8','binary','gzip')),
    sensitive     INTEGER NOT NULL DEFAULT 0 CHECK (sensitive IN (0,1)),
    created_at    INTEGER NOT NULL,
    UNIQUE (engagement_id, sha256, rel_path)
);

CREATE TABLE assets (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    parent_id     TEXT REFERENCES assets(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('domain','subdomain','ip','host','url','endpoint','repo','bucket','mobile_app','api')),
    value         TEXT NOT NULL,
    display       TEXT,
    state         TEXT NOT NULL DEFAULT 'unknown' CHECK (state IN ('unknown','alive','dead','filtered')),
    confidence    REAL NOT NULL DEFAULT 1.0 CHECK (confidence BETWEEN 0.0 AND 1.0),
    first_seen_at INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    source        TEXT NOT NULL,
    attrs_json    TEXT NOT NULL DEFAULT '{}',
    tags_json     TEXT,
    UNIQUE (engagement_id, kind, value)
);

CREATE INDEX idx_assets_eng_kind_state ON assets(engagement_id, kind, state);
CREATE INDEX idx_assets_parent ON assets(parent_id);

CREATE TABLE services (
    id                 TEXT PRIMARY KEY,
    asset_id           TEXT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    port               INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    protocol           TEXT NOT NULL CHECK (protocol IN ('tcp','udp')),
    state              TEXT NOT NULL CHECK (state IN ('open','closed','filtered','open|filtered')),
    service            TEXT,
    product            TEXT,
    version            TEXT,
    banner             TEXT,
    tls_json           TEXT,
    source_tool_run_id TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    updated_at         INTEGER NOT NULL,
    UNIQUE (asset_id, port, protocol)
);

-- ---------------------------------------------------------------------------
-- Semantic memory: facts with supersession, and the FTS5 index over them.
-- ---------------------------------------------------------------------------

CREATE TABLE facts (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    subject       TEXT NOT NULL,
    predicate     TEXT NOT NULL,
    value         TEXT NOT NULL,
    value_type    TEXT NOT NULL DEFAULT 'text' CHECK (value_type IN ('text','number','bool','json','ref')),
    confidence    REAL NOT NULL DEFAULT 0.8 CHECK (confidence BETWEEN 0.0 AND 1.0),
    source_kind   TEXT NOT NULL CHECK (source_kind IN ('tool_run','llm_inference','operator','osint')),
    source_id     TEXT,
    stage         TEXT NOT NULL,
    superseded_by TEXT REFERENCES facts(id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL
);

CREATE INDEX idx_facts_lookup ON facts(engagement_id, subject, predicate, superseded_by);

CREATE VIRTUAL TABLE facts_fts USING fts5(
    value, subject, predicate,
    content='facts', content_rowid='rowid', tokenize='porter unicode61'
);

CREATE TRIGGER facts_ai AFTER INSERT ON facts BEGIN
    INSERT INTO facts_fts(rowid, value, subject, predicate) VALUES (new.rowid, new.value, new.subject, new.predicate);
END;

CREATE TRIGGER facts_ad AFTER DELETE ON facts BEGIN
    INSERT INTO facts_fts(facts_fts, rowid, value, subject, predicate) VALUES ('delete', old.rowid, old.value, old.subject, old.predicate);
END;

CREATE TRIGGER facts_au AFTER UPDATE ON facts BEGIN
    INSERT INTO facts_fts(facts_fts, rowid, value, subject, predicate) VALUES ('delete', old.rowid, old.value, old.subject, old.predicate);
    INSERT INTO facts_fts(rowid, value, subject, predicate) VALUES (new.rowid, new.value, new.subject, new.predicate);
END;

-- ---------------------------------------------------------------------------
-- Findings and the evidence that proves them.
-- ---------------------------------------------------------------------------

CREATE TABLE findings (
    id                        TEXT PRIMARY KEY,
    engagement_id             TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    stage_run_id              TEXT REFERENCES stage_runs(id) ON DELETE SET NULL,
    title                     TEXT NOT NULL,
    category                  TEXT NOT NULL CHECK (category IN ('web','network','cloud','mobile','crypto','auth','config','data_exposure','access_control')),
    wstg_id                   TEXT,
    attack_technique_ids_json TEXT,
    severity                  TEXT NOT NULL CHECK (severity IN ('critical','high','medium','low','info')),
    confidence                TEXT NOT NULL CHECK (confidence IN ('confirmed','likely','possible')),
    cvss_vector               TEXT,
    cvss_score                REAL CHECK (cvss_score IS NULL OR (cvss_score BETWEEN 0.0 AND 10.0)),
    asset_id                  TEXT REFERENCES assets(id) ON DELETE SET NULL,
    endpoint                  TEXT,
    parameter                 TEXT,
    description               TEXT NOT NULL,
    impact                    TEXT,
    remediation               TEXT,
    reproduction              TEXT,
    status                    TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','false_positive','accepted_risk','fixed','retest')),
    verified                  INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0,1)),
    created_at                INTEGER NOT NULL,
    updated_at                INTEGER NOT NULL
);

CREATE INDEX idx_findings_sev ON findings(engagement_id, severity);
CREATE INDEX idx_findings_status ON findings(engagement_id, status);

CREATE TABLE evidence (
    id             TEXT PRIMARY KEY,
    finding_id     TEXT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    tool_run_id    TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    llm_call_id    TEXT REFERENCES llm_calls(id) ON DELETE SET NULL,
    artifact_id    TEXT REFERENCES artifacts(id) ON DELETE SET NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('poc','observation','screenshot','output_excerpt','reasoning')),
    excerpt        TEXT,
    command        TEXT,
    captured_at    INTEGER NOT NULL,
    output_sha256  TEXT,
    note           TEXT,
    CHECK (tool_run_id IS NOT NULL OR llm_call_id IS NOT NULL OR artifact_id IS NOT NULL)
);

-- ---------------------------------------------------------------------------
-- The Mapper stage's attack graph.
-- ---------------------------------------------------------------------------

CREATE TABLE attack_nodes (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    asset_id      TEXT REFERENCES assets(id) ON DELETE SET NULL,
    node_key      TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('asset','service','credential','session','vuln','goal','data')),
    label         TEXT NOT NULL,
    attrs_json    TEXT,
    reachable     INTEGER NOT NULL DEFAULT 1 CHECK (reachable IN (0,1)),
    priority      REAL NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    UNIQUE (engagement_id, node_key)
);

CREATE TABLE attack_edges (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    from_node_id  TEXT NOT NULL REFERENCES attack_nodes(id) ON DELETE CASCADE,
    to_node_id    TEXT NOT NULL REFERENCES attack_nodes(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('can_reach','exploits','yields','authenticates_to','pivots_to','exfiltrates')),
    technique     TEXT,
    cost          REAL NOT NULL DEFAULT 1.0,
    confidence    REAL NOT NULL DEFAULT 0.5 CHECK (confidence BETWEEN 0.0 AND 1.0),
    finding_id    TEXT REFERENCES findings(id) ON DELETE SET NULL,
    created_at    INTEGER NOT NULL,
    UNIQUE (engagement_id, from_node_id, to_node_id, kind)
);

-- ERD.md section 6: reverse traversal of the attack graph.
CREATE INDEX idx_attack_edges_to ON attack_edges(engagement_id, to_node_id);

-- ---------------------------------------------------------------------------
-- Exploitation: credentials, footholds, post-exploitation, approvals.
-- ---------------------------------------------------------------------------

CREATE TABLE credentials (
    id                 TEXT PRIMARY KEY,
    engagement_id      TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    asset_id           TEXT REFERENCES assets(id) ON DELETE SET NULL,
    username           TEXT,
    secret_ciphertext  BLOB NOT NULL,
    secret_nonce       BLOB NOT NULL,
    secret_kdf         TEXT NOT NULL CHECK (secret_kdf IN ('argon2id','os_keyring')),
    secret_hint        TEXT,
    kind               TEXT NOT NULL CHECK (kind IN ('password','ntlm','ssh_key','token','cookie','cert')),
    source_tool_run_id TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    valid_until        INTEGER,
    verified           INTEGER NOT NULL DEFAULT 0 CHECK (verified IN (0,1)),
    created_at         INTEGER NOT NULL
);

CREATE TABLE access_sessions (
    id                    TEXT PRIMARY KEY,
    engagement_id         TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    parent_id             TEXT REFERENCES access_sessions(id) ON DELETE SET NULL,
    asset_id              TEXT REFERENCES assets(id) ON DELETE SET NULL,
    label                 TEXT NOT NULL,
    transport             TEXT NOT NULL CHECK (transport IN ('reverse_shell','bind_shell','ssh','winrm','smb','web_shell','meterpreter','rce')),
    credential_id         TEXT REFERENCES credentials(id) ON DELETE SET NULL,
    privilege             TEXT CHECK (privilege IS NULL OR privilege IN ('user','sudo','system','domain_admin','root')),
    status                TEXT NOT NULL CHECK (status IN ('active','lost','closed')),
    opened_by_tool_run_id TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    opened_at             INTEGER NOT NULL,
    closed_at             INTEGER,
    notes                 TEXT
);

CREATE TABLE post_exploit_actions (
    id                  TEXT PRIMARY KEY,
    access_session_id   TEXT NOT NULL REFERENCES access_sessions(id) ON DELETE CASCADE,
    tool_run_id         TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    seq                 INTEGER NOT NULL,
    action              TEXT NOT NULL CHECK (action IN ('enumerate','escalate','dump','pivot','persist','exfil','cleanup')),
    attack_technique_id TEXT,
    command             TEXT,
    result_summary      TEXT,
    artifact_id         TEXT REFERENCES artifacts(id) ON DELETE SET NULL,
    success             INTEGER NOT NULL CHECK (success IN (0,1)),
    created_at          INTEGER NOT NULL,
    UNIQUE (access_session_id, seq)
);

CREATE TABLE approvals (
    id                       TEXT PRIMARY KEY,
    engagement_id            TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    tool_run_id              TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    requested_by_llm_call_id TEXT REFERENCES llm_calls(id) ON DELETE SET NULL,
    kind                     TEXT NOT NULL CHECK (kind IN ('destructive_action','exploit_launch','credential_use','data_access')),
    command_preview          TEXT NOT NULL,
    reason                   TEXT NOT NULL,
    decision                 TEXT NOT NULL CHECK (decision IN ('pending','approved','denied','expired')),
    decided_at               INTEGER,
    decided_by               TEXT,
    expires_at               INTEGER NOT NULL,
    created_at               INTEGER NOT NULL
);

CREATE TRIGGER trg_approvals_decision_immutable
BEFORE UPDATE OF decision ON approvals
WHEN OLD.decision <> 'pending' AND NEW.decision <> OLD.decision
BEGIN
    SELECT RAISE(ABORT, 'approval decision is immutable');
END;

-- ---------------------------------------------------------------------------
-- Procedural memory: skills and RAG chunks.
-- ---------------------------------------------------------------------------

CREATE TABLE skills (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    category            TEXT NOT NULL CHECK (category IN ('recon','osint','web','network','cloud','mobile','exploit','post','report','meta')),
    description         TEXT NOT NULL,
    version             TEXT,
    path                TEXT NOT NULL UNIQUE,
    sha256              TEXT NOT NULL,
    stage_affinity_json TEXT,
    tags_json           TEXT,
    when_to_use         TEXT,
    token_estimate      INTEGER NOT NULL DEFAULT 0,
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    indexed_at          INTEGER NOT NULL
);

CREATE TABLE memory_chunks (
    id              TEXT PRIMARY KEY,
    engagement_id   TEXT REFERENCES engagements(id) ON DELETE CASCADE,
    source_kind     TEXT NOT NULL CHECK (source_kind IN ('skill','fact','event','finding','artifact_excerpt','lesson')),
    source_id       TEXT NOT NULL,
    chunk_index     INTEGER NOT NULL DEFAULT 0,
    text            TEXT NOT NULL,
    token_estimate  INTEGER NOT NULL,
    embedding_model TEXT,
    embedding_dim   INTEGER,
    embedding       BLOB,
    created_at      INTEGER NOT NULL,
    UNIQUE (source_kind, source_id, chunk_index, embedding_model)
);

CREATE INDEX idx_chunks_eng_source ON memory_chunks(engagement_id, source_kind);

CREATE VIRTUAL TABLE chunks_fts USING fts5(
    text, content='memory_chunks', content_rowid='rowid', tokenize='porter unicode61'
);

CREATE TRIGGER chunks_ai AFTER INSERT ON memory_chunks BEGIN
    INSERT INTO chunks_fts(rowid, text) VALUES (new.rowid, new.text);
END;

CREATE TRIGGER chunks_ad AFTER DELETE ON memory_chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
END;

CREATE TRIGGER chunks_au AFTER UPDATE ON memory_chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
    INSERT INTO chunks_fts(rowid, text) VALUES (new.rowid, new.text);
END;

-- ---------------------------------------------------------------------------
-- Episodic memory: the append-only event feed and its FTS5 index.
-- ---------------------------------------------------------------------------

CREATE TABLE events (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    stage         TEXT,
    session_id    TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    tool_run_id   TEXT REFERENCES tool_runs(id) ON DELETE SET NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('stage_start','stage_end','tool_start','tool_end','llm_call','fact_set','finding_added','approval_requested','approval_decided','session_opened','error','note','operator_command')),
    level         TEXT NOT NULL CHECK (level IN ('debug','info','warn','error')),
    message       TEXT NOT NULL,
    detail_json   TEXT,
    created_at    INTEGER NOT NULL
);

CREATE INDEX idx_events_eng_time ON events(engagement_id, created_at);
CREATE INDEX idx_events_kind_time ON events(kind, created_at);

CREATE VIRTUAL TABLE events_fts USING fts5(
    message, content='events', content_rowid='rowid', tokenize='porter unicode61'
);

-- `events` has no UPDATE or DELETE path (enforced below), so the insert path is
-- the only one that can change the index.
CREATE TRIGGER events_ai AFTER INSERT ON events BEGIN
    INSERT INTO events_fts(rowid, message) VALUES (new.rowid, new.message);
END;

CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;

CREATE TRIGGER events_no_delete BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;

-- ---------------------------------------------------------------------------
-- The hash-chained audit trail. Deliberately has no FK to engagements so the
-- legal record survives engagement deletion.
-- ---------------------------------------------------------------------------

CREATE TABLE audit_trail (
    id            TEXT PRIMARY KEY,
    engagement_id TEXT,
    actor         TEXT NOT NULL CHECK (actor IN ('operator','agent','system')),
    action        TEXT NOT NULL,
    target        TEXT,
    command       TEXT,
    outcome       TEXT NOT NULL CHECK (outcome IN ('allowed','denied','error')),
    detail_json   TEXT,
    prev_hash     TEXT,
    entry_hash    TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE INDEX idx_audit_time ON audit_trail(created_at);

CREATE TRIGGER audit_no_update BEFORE UPDATE ON audit_trail
BEGIN SELECT RAISE(ABORT, 'audit_trail is append-only'); END;

CREATE TRIGGER audit_no_delete BEFORE DELETE ON audit_trail
BEGIN SELECT RAISE(ABORT, 'audit_trail is append-only'); END;

-- ---------------------------------------------------------------------------
-- Reporting and budget accounting.
-- ---------------------------------------------------------------------------

CREATE TABLE reports (
    id                   TEXT PRIMARY KEY,
    engagement_id        TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    format               TEXT NOT NULL CHECK (format IN ('markdown','html')),
    template_version     TEXT NOT NULL,
    artifact_id          TEXT REFERENCES artifacts(id) ON DELETE SET NULL,
    finding_count        INTEGER NOT NULL,
    severity_counts_json TEXT NOT NULL,
    generated_at         INTEGER NOT NULL,
    db_snapshot_hash     TEXT NOT NULL
);

CREATE TABLE usage_ledger (
    id              TEXT PRIMARY KEY,
    engagement_id   TEXT NOT NULL REFERENCES engagements(id) ON DELETE CASCADE,
    stage           TEXT,
    kind            TEXT NOT NULL CHECK (kind IN ('llm','tool')),
    ref_id          TEXT NOT NULL,
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    cost_usd_micros INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL
);

CREATE INDEX idx_usage_eng ON usage_ledger(engagement_id);
