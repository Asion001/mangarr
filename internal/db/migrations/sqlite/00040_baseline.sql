-- +goose Up
-- The schema as of 1.0: the 40 migrations before it, squashed.

CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    group_id INTEGER REFERENCES groups (id),
    reader_id INTEGER REFERENCES readers (id) ON DELETE SET NULL,
    display_name TEXT NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at TIMESTAMP,
    oidc_subject TEXT NOT NULL DEFAULT '',
    created_by INTEGER
);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL UNIQUE
);

CREATE TABLE root_folders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    language TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    managed_by TEXT NOT NULL DEFAULT ''
);

CREATE TABLE profiles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    config TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE provider_definitions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    implementation TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    priority INTEGER NOT NULL DEFAULT 25,
    tags TEXT NOT NULL DEFAULT '[]',
    events TEXT NOT NULL DEFAULT '[]',
    settings TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    managed_by TEXT NOT NULL DEFAULT '',
    user_id INTEGER REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE series (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    sort_title TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    monitored BOOLEAN NOT NULL DEFAULT TRUE,
    monitor_new TEXT NOT NULL DEFAULT 'all',
    root_folder_id INTEGER NOT NULL REFERENCES root_folders (id),
    path TEXT NOT NULL,
    profile_id INTEGER NOT NULL REFERENCES profiles (id),
    language TEXT NOT NULL DEFAULT '',
    reading_direction TEXT NOT NULL DEFAULT 'rtl',
    tags TEXT NOT NULL DEFAULT '[]',
    metadata TEXT NOT NULL DEFAULT '{}',
    add_options TEXT NOT NULL DEFAULT '{}',
    added_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    last_metadata_refresh TIMESTAMP,
    blocked_scanlators TEXT NOT NULL DEFAULT '[]',
    work_id INTEGER REFERENCES works (id),
    source_priority_mode TEXT NOT NULL DEFAULT 'custom' CHECK (source_priority_mode IN ('inherit','custom')),
    preview BOOLEAN NOT NULL DEFAULT false,
    preview_seen_at TIMESTAMP
);

CREATE TABLE series_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    module_id INTEGER NOT NULL,
    source_id TEXT NOT NULL,
    source_name TEXT NOT NULL DEFAULT '',
    lang TEXT NOT NULL DEFAULT '',
    manga_url TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    web_url TEXT NOT NULL DEFAULT '',
    engine_ref TEXT NOT NULL DEFAULT '',
    priority INTEGER NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    check_interval_minutes INTEGER NOT NULL DEFAULT 0,
    last_checked_at TIMESTAMP,
    last_success_at TIMESTAMP,
    next_check_at TIMESTAMP NOT NULL,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    backoff_until TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE chapters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    number_key TEXT NOT NULL,
    number_sort REAL NOT NULL,
    volume TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    monitored BOOLEAN NOT NULL DEFAULT TRUE,
    state TEXT NOT NULL DEFAULT 'missing',
    file_id INTEGER,
    cleaned_at TIMESTAMP,
    release_date TIMESTAMP,
    first_seen_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE chapter_releases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id INTEGER REFERENCES chapters (id) ON DELETE SET NULL,
    series_source_id INTEGER NOT NULL REFERENCES series_sources (id) ON DELETE CASCADE,
    chapter_url TEXT NOT NULL,
    web_url TEXT NOT NULL DEFAULT '',
    engine_ref TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    scanlator TEXT NOT NULL DEFAULT '',
    raw_number REAL NOT NULL DEFAULT -1,
    upload_date TIMESTAMP,
    removed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE chapter_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL,
    size INTEGER NOT NULL DEFAULT 0,
    page_count INTEGER NOT NULL DEFAULT 0,
    avg_width INTEGER NOT NULL DEFAULT 0,
    format TEXT NOT NULL DEFAULT '',
    release_id INTEGER,
    scanlator TEXT NOT NULL DEFAULT '',
    source_name TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL DEFAULT '',
    upscaled BOOLEAN NOT NULL DEFAULT FALSE,
    upscale_model TEXT NOT NULL DEFAULT '',
    size_before INTEGER NOT NULL DEFAULT 0,
    imported_at TIMESTAMP NOT NULL,
    process_params TEXT NOT NULL DEFAULT '',
    process_state TEXT NOT NULL DEFAULT '',
    process_attempts INTEGER NOT NULL DEFAULT 0,
    process_retry_at TIMESTAMP,
    process_error TEXT NOT NULL DEFAULT '',
    processed_at TIMESTAMP,
    size_original INTEGER NOT NULL DEFAULT 0,
    process_seconds REAL NOT NULL DEFAULT 0,
    process_pages INTEGER NOT NULL DEFAULT 0,
    source_pages TEXT
);

CREATE TABLE download_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL DEFAULT 'download',
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    release_id INTEGER,
    status TEXT NOT NULL,
    progress INTEGER NOT NULL DEFAULT 0,
    pages_done INTEGER NOT NULL DEFAULT 0,
    pages_total INTEGER NOT NULL DEFAULT 0,
    attempt INTEGER NOT NULL DEFAULT 0,
    is_upgrade BOOLEAN NOT NULL DEFAULT FALSE,
    error TEXT NOT NULL DEFAULT '',
    not_before TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    priority INTEGER NOT NULL DEFAULT 0,
    rank INTEGER NOT NULL DEFAULT 0,
    recycled_file_id BIGINT,
    config_override TEXT,
    force_download BOOLEAN NOT NULL DEFAULT FALSE,
    profile_name TEXT NOT NULL DEFAULT '',
    pin_release BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL,
    chapter_id INTEGER,
    event_type TEXT NOT NULL,
    source_title TEXT NOT NULL DEFAULT '',
    data TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE blocklist (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id INTEGER,
    series_source_id INTEGER NOT NULL,
    chapter_url TEXT NOT NULL,
    scanlator TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE commands (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    "trigger" TEXT NOT NULL DEFAULT 'manual',
    message TEXT NOT NULL DEFAULT '',
    queued_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    ended_at TIMESTAMP,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE scheduled_tasks (
    name TEXT PRIMARY KEY,
    interval_minutes INTEGER NOT NULL,
    last_execution TIMESTAMP,
    last_start TIMESTAMP,
    paused BOOLEAN NOT NULL DEFAULT false,
    schedule_kind TEXT,
    custom_interval_minutes INTEGER,
    times_of_day TEXT,
    weekdays TEXT,
    updated_at TIMESTAMP
);

CREATE TABLE readers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    count_for_cleanup BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE reader_accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reader_id INTEGER NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    module_id INTEGER NOT NULL,
    credentials TEXT NOT NULL DEFAULT '{}',
    external_user TEXT NOT NULL DEFAULT '',
    last_sync_at TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE catalog_prefs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    module_id INTEGER NOT NULL REFERENCES provider_definitions (id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    priority INTEGER NOT NULL DEFAULT 100,
    throttle TEXT NOT NULL DEFAULT '{}',
    cooldown_until TIMESTAMP,
    cooldown_strikes INTEGER NOT NULL DEFAULT 0,
    last_throttle TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (module_id, source_id)
);

CREATE TABLE imports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    format TEXT NOT NULL,
    file_name TEXT NOT NULL,
    status TEXT NOT NULL,
    progress TEXT NOT NULL DEFAULT '',
    options TEXT NOT NULL DEFAULT '{}',
    info TEXT NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE import_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    import_id INTEGER NOT NULL REFERENCES imports (id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title TEXT NOT NULL,
    state TEXT NOT NULL,
    selected BOOLEAN NOT NULL DEFAULT TRUE,
    data TEXT NOT NULL DEFAULT '{}',
    source TEXT,
    metadata TEXT,
    extension TEXT,
    series_id INTEGER REFERENCES series (id) ON DELETE SET NULL,
    message TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE reading_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_hash TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    last_client TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP,
    user_id INTEGER REFERENCES users (id) ON DELETE CASCADE,
    koreader_hash TEXT NOT NULL DEFAULT '',
    languages TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE read_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reader_id INTEGER NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    series_id INTEGER NOT NULL,
    chapter_id INTEGER NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    page INTEGER NOT NULL DEFAULT 0,
    origin TEXT NOT NULL,
    client TEXT NOT NULL DEFAULT '',
    device TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    at TIMESTAMP NOT NULL,
    chapters INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    builtin TEXT NOT NULL DEFAULT '',
    permissions TEXT NOT NULL DEFAULT '[]',
    include_tags TEXT NOT NULL DEFAULT '[]',
    exclude_tags TEXT NOT NULL DEFAULT '[]',
    root_folders TEXT NOT NULL DEFAULT '[]',
    auto_approve_requests BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    max_rating TEXT NOT NULL DEFAULT '',
    blocked_genres TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);

CREATE TABLE invites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT NOT NULL UNIQUE,
    group_id INTEGER NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    note TEXT NOT NULL DEFAULT '',
    max_uses INTEGER NOT NULL DEFAULT 1,
    uses INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMP,
    created_by INTEGER,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE reader_prefs (
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    series_id INTEGER NOT NULL DEFAULT 0,
    data TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, series_id)
);

CREATE TABLE requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    series_id INTEGER REFERENCES series (id) ON DELETE SET NULL,
    handled_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    handled_at TIMESTAMP,
    available_at TIMESTAMP,
    language TEXT NOT NULL DEFAULT '',
    work_id INTEGER,
    kind TEXT NOT NULL DEFAULT ''
);

CREATE TABLE request_users (
    request_id INTEGER NOT NULL REFERENCES requests (id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (request_id, user_id)
);

CREATE TABLE follows (
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, series_id)
);

CREATE TABLE workers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    key_hash TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    roles TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    version TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL DEFAULT '',
    info TEXT NOT NULL DEFAULT '{}',
    last_ip TEXT NOT NULL DEFAULT '',
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP,
    tasks_done INTEGER NOT NULL DEFAULT 0,
    tasks_failed INTEGER NOT NULL DEFAULT 0,
    pages_done INTEGER NOT NULL DEFAULT 0,
    bytes_in INTEGER NOT NULL DEFAULT 0,
    bytes_out INTEGER NOT NULL DEFAULT 0,
    busy_seconds REAL NOT NULL DEFAULT 0,
    priority INTEGER NOT NULL DEFAULT 100,
    concurrent INTEGER NOT NULL DEFAULT 0,
    upscale_model TEXT NOT NULL DEFAULT '',
    page_concurrency INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE worker_tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id INTEGER NOT NULL REFERENCES download_jobs (id) ON DELETE CASCADE,
    worker_id INTEGER REFERENCES workers (id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    seq INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL,
    spec TEXT NOT NULL DEFAULT '{}',
    not_before TIMESTAMP NOT NULL,
    lease_until TIMESTAMP,
    heartbeat_at TIMESTAMP,
    cancel INTEGER NOT NULL DEFAULT 0,
    attempt INTEGER NOT NULL DEFAULT 0,
    pages_total INTEGER NOT NULL DEFAULT 0,
    pages_done INTEGER NOT NULL DEFAULT 0,
    bytes_in INTEGER NOT NULL DEFAULT 0,
    bytes_out INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE TABLE user_ui_preferences (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    locale TEXT NOT NULL DEFAULT 'auto' CHECK (locale IN ('auto','en','ru','uk')),
    mode TEXT NOT NULL DEFAULT 'reading' CHECK (mode IN ('reading','editing')),
    updated_at TIMESTAMP NOT NULL,
    options TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE works (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    sort_title TEXT NOT NULL,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE source_priority_lists (
    scope TEXT PRIMARY KEY,
    sources TEXT NOT NULL DEFAULT '[]'
);

CREATE TABLE reading_sessions (
    id TEXT PRIMARY KEY,
    reader_id INTEGER NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    active_seconds INTEGER NOT NULL DEFAULT 0 CHECK (active_seconds >= 0),
    started_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE messenger_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    external_id TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'instant',
    events TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'active',
    last_error TEXT NOT NULL DEFAULT '',
    last_attempt_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    digest_through TIMESTAMP,
    UNIQUE (user_id, kind),
    UNIQUE (kind, external_id)
);

CREATE TABLE messenger_link_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT NOT NULL UNIQUE,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE notification_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    dedupe_key TEXT NOT NULL,
    event_type TEXT NOT NULL,
    series_id INTEGER REFERENCES series (id) ON DELETE SET NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    UNIQUE (user_id, dedupe_key)
);

CREATE TABLE notification_dispatches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id INTEGER NOT NULL REFERENCES notification_deliveries (id) ON DELETE CASCADE,
    link_id INTEGER NOT NULL REFERENCES messenger_links (id) ON DELETE CASCADE,
    available_at TIMESTAMP NOT NULL,
    sent_at TIMESTAMP,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    UNIQUE (delivery_id, link_id)
);

CREATE TABLE koreader_documents (
    reader_id INTEGER NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    document TEXT NOT NULL,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    PRIMARY KEY (reader_id, document)
);

CREATE TABLE download_queue_order (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE recycled_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id BIGINT REFERENCES series(id) ON DELETE SET NULL,
    chapter_id BIGINT REFERENCES chapters(id) ON DELETE SET NULL,
    chapter_file_id BIGINT,
    kind TEXT NOT NULL,
    reason TEXT NOT NULL,
    job_id BIGINT,
    job_kind TEXT NOT NULL DEFAULT '',
    profile_name TEXT NOT NULL DEFAULT '',
    process_params TEXT NOT NULL DEFAULT '',
    series_title TEXT NOT NULL DEFAULT '',
    original_relative_path TEXT NOT NULL,
    recycled_path TEXT NOT NULL UNIQUE,
    size BIGINT NOT NULL DEFAULT 0,
    page_count INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    source_name TEXT NOT NULL DEFAULT '',
    scanlator TEXT NOT NULL DEFAULT '',
    file_snapshot TEXT,
    folder_snapshot TEXT,
    recycled_at DATETIME NOT NULL
);

CREATE TABLE title_read_states (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    reader_id INTEGER NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    title_id INTEGER NOT NULL,
    number_key TEXT NOT NULL,
    chapter_id INTEGER NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    page INTEGER NOT NULL DEFAULT 0,
    read_at TIMESTAMP,
    synced_at TIMESTAMP NOT NULL,
    origin TEXT NOT NULL DEFAULT ''
);

CREATE TABLE page_bounds (
    file_id INTEGER NOT NULL REFERENCES chapter_files (id) ON DELETE CASCADE,
    page INTEGER NOT NULL,
    sha256 TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    x INTEGER NOT NULL,
    y INTEGER NOT NULL,
    w INTEGER NOT NULL,
    h INTEGER NOT NULL,
    PRIMARY KEY (file_id, page)
);

CREATE INDEX provider_definitions_kind ON provider_definitions (kind);
CREATE UNIQUE INDEX series_root_path ON series (root_folder_id, path);
CREATE UNIQUE INDEX series_sources_identity ON series_sources (series_id, source_id, manga_url);
CREATE INDEX series_sources_next_check ON series_sources (next_check_at);
CREATE UNIQUE INDEX chapters_series_number ON chapters (series_id, number_key);
CREATE INDEX chapters_state ON chapters (state);
CREATE UNIQUE INDEX chapter_releases_identity ON chapter_releases (series_source_id, chapter_url);
CREATE INDEX chapter_releases_chapter ON chapter_releases (chapter_id);
CREATE UNIQUE INDEX chapter_files_chapter ON chapter_files (chapter_id);
CREATE INDEX download_jobs_status ON download_jobs (status);
CREATE INDEX history_series ON history (series_id);
CREATE INDEX history_created ON history (created_at);
CREATE UNIQUE INDEX blocklist_identity ON blocklist (series_source_id, chapter_url);
CREATE INDEX commands_status ON commands (status);
CREATE UNIQUE INDEX reader_accounts_identity ON reader_accounts (reader_id, module_id);
CREATE UNIQUE INDEX download_jobs_active_chapter ON download_jobs (chapter_id) WHERE status IN ('queued', 'paused', 'downloading', 'processing', 'importing');
CREATE INDEX chapter_files_process ON chapter_files (process_params, process_retry_at);
CREATE INDEX import_entries_import ON import_entries (import_id, position);
CREATE INDEX chapter_files_processed_at ON chapter_files (processed_at);
CREATE INDEX read_events_reader ON read_events (reader_id, at);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX requests_status ON requests (status);
CREATE INDEX follows_series ON follows (series_id);
CREATE INDEX worker_tasks_waiting ON worker_tasks (state, kind, not_before);
CREATE INDEX worker_tasks_leases ON worker_tasks (state, lease_until);
CREATE INDEX worker_tasks_job ON worker_tasks (job_id);
CREATE INDEX worker_tasks_worker ON worker_tasks (worker_id, state);
CREATE INDEX series_work ON series (work_id);
CREATE INDEX chapters_first_seen ON chapters (first_seen_at, id);
CREATE INDEX series_added ON series (added_at, id);
CREATE INDEX works_created ON works (created_at, id);
CREATE INDEX reading_sessions_reader_updated ON reading_sessions (reader_id, updated_at);
CREATE INDEX messenger_link_tokens_expiry ON messenger_link_tokens (expires_at);
CREATE INDEX notification_deliveries_user_created ON notification_deliveries (user_id, created_at DESC);
CREATE INDEX notification_dispatches_pending ON notification_dispatches (sent_at, available_at);
CREATE INDEX koreader_documents_chapter ON koreader_documents (chapter_id);
CREATE INDEX download_jobs_dispatch ON download_jobs (status, rank, id);
CREATE INDEX download_jobs_rank ON download_jobs (rank, id);
CREATE INDEX download_jobs_enqueue_priority ON download_jobs (priority, rank) WHERE status IN ('queued', 'paused');
CREATE INDEX download_jobs_list ON download_jobs ( CASE status WHEN 'importing' THEN 0 WHEN 'processing' THEN 1 WHEN 'downloading' THEN 2 WHEN 'queued' THEN 3 WHEN 'paused' THEN 3 WHEN 'failed' THEN 4 ELSE 5 END, rank, id );
CREATE INDEX commands_name_id ON commands (name, id DESC);
CREATE INDEX recycled_files_series ON recycled_files(series_id, recycled_at);
CREATE INDEX recycled_files_chapter ON recycled_files(chapter_id, recycled_at);
CREATE INDEX recycled_files_retention ON recycled_files(recycled_at);
CREATE INDEX download_jobs_recycled ON download_jobs(recycled_file_id);
CREATE INDEX series_preview ON series (preview);
CREATE UNIQUE INDEX title_read_states_identity ON title_read_states (reader_id, title_id, number_key);
CREATE INDEX title_read_states_title ON title_read_states (title_id, number_key);
CREATE INDEX series_title ON series ((COALESCE(work_id, -id)));

CREATE VIEW chapter_read_states AS
SELECT t.id, t.reader_id, c.id AS chapter_id, c.series_id, t.completed, t.page, t.read_at, t.synced_at, t.origin,
       t.chapter_id AS source_chapter_id
FROM title_read_states AS t
JOIN series AS s ON COALESCE(s.work_id, -s.id) = t.title_id
JOIN chapters AS c ON c.series_id = s.id AND c.number_key = t.number_key;

INSERT INTO download_queue_order (id) VALUES (1);
