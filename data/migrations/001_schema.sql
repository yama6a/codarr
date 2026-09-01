-- +migrate Up
--
-- Consolidated starting schema from plan.md 17.1. Frozen: corrections ship as a new file.
-- idx_jobs_one_active_per_file is a PARTIAL unique index, which is what makes enqueue idempotent.
-- media_files.codarr_output_fingerprint IS NULL is the only record that Codarr never wrote a file (12).

-- Configuration, edited via the UI. Single row.
CREATE TABLE settings (
  id                       INTEGER PRIMARY KEY CHECK (id = 1),
  temp_dir                 TEXT NOT NULL,
  qsv_device               TEXT NOT NULL DEFAULT '/dev/dri/renderD128',
  scan_enabled             INTEGER NOT NULL DEFAULT 1,
  scan_cron                TEXT NOT NULL DEFAULT '0 4 * * *',
  scan_rate_limit_fps      INTEGER NOT NULL DEFAULT 50,
  queue_paused             INTEGER NOT NULL DEFAULT 0,
  prioritise_quick_jobs    INTEGER NOT NULL DEFAULT 1,
  full_hash_enabled        INTEGER NOT NULL DEFAULT 0,   -- 12.2
  updated_at               TIMESTAMP NOT NULL
);

CREATE TABLE plex (
  id                    INTEGER PRIMARY KEY CHECK (id = 1),
  base_url              TEXT NOT NULL,
  token                 TEXT,
  client_identifier     TEXT NOT NULL,
  refresh_after         INTEGER NOT NULL DEFAULT 1,
  analyze_after         INTEGER NOT NULL DEFAULT 1,
  guard_active_streams  INTEGER NOT NULL DEFAULT 1,
  last_tested_at        TIMESTAMP,
  last_test_result      TEXT,
  updated_at            TIMESTAMP NOT NULL
);

CREATE TABLE plex_path_mappings (
  id     INTEGER PRIMARY KEY,
  local  TEXT NOT NULL,
  remote TEXT NOT NULL,
  sort   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE arr_instances (
  id               INTEGER PRIMARY KEY,
  name             TEXT NOT NULL UNIQUE,
  flavour          TEXT NOT NULL,        -- radarr | sonarr
  base_url         TEXT NOT NULL,
  api_key          TEXT NOT NULL,
  webhook_id       TEXT NOT NULL UNIQUE,
  rescan_after     INTEGER NOT NULL DEFAULT 1,
  unmonitor_after  INTEGER NOT NULL DEFAULT 0,
  enabled          INTEGER NOT NULL DEFAULT 1,
  last_tested_at   TIMESTAMP,
  last_test_result TEXT,
  created_at       TIMESTAMP NOT NULL,
  updated_at       TIMESTAMP NOT NULL
);

CREATE TABLE arr_path_mappings (
  id              INTEGER PRIMARY KEY,
  arr_instance_id INTEGER NOT NULL REFERENCES arr_instances(id) ON DELETE CASCADE,
  local           TEXT NOT NULL,
  remote          TEXT NOT NULL,
  sort            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE roots (
  id              INTEGER PRIMARY KEY,
  path            TEXT NOT NULL UNIQUE,
  arr_instance_id INTEGER REFERENCES arr_instances(id) ON DELETE SET NULL,
  imported        INTEGER NOT NULL DEFAULT 0,
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      TIMESTAMP NOT NULL
);

CREATE TABLE media_files (
  id                 INTEGER PRIMARY KEY,
  path               TEXT NOT NULL UNIQUE,
  root_id            INTEGER REFERENCES roots(id) ON DELETE SET NULL,
  arr_instance_id    INTEGER REFERENCES arr_instances(id) ON DELETE SET NULL,
  arr_entity_id      INTEGER,
  size_bytes         INTEGER NOT NULL,
  mtime              INTEGER NOT NULL,
  nlink              INTEGER,
  fingerprint        TEXT,
  probe_json         TEXT,          -- full ffprobe output
  -- Never written: internal/api/mediainfo.go derives the same summary from probe_json
  -- on read. Kept only to match plan.md 17.1, do not start populating it.
  media_info_json    TEXT,          -- parsed summary for the UI modal, unused
  analyzed_at        TIMESTAMP,
  plan_json          TEXT,
  plan_kind          TEXT,
  plan_reasons       TEXT,          -- JSON array of strings
  container          TEXT,
  video_codec        TEXT,          -- denormalised for library filtering
  video_profile      TEXT,
  video_level        TEXT,
  video_bitrate      INTEGER,
  video_bitrate_src  TEXT,          -- which rung of 8.4 produced it
  is_hdr             INTEGER NOT NULL DEFAULT 0,
  fingerprint_algo   TEXT,          -- 'xxh3-128' - lets the algorithm change later
  codarr_tagged      INTEGER NOT NULL DEFAULT 0,
  codarr_policy_hash TEXT,
  -- Output identity, written at promotion (section 12). NULL means Codarr has
  -- never written this file.
  codarr_job_id            INTEGER REFERENCES jobs(id) ON DELETE SET NULL,
  codarr_processed_at      TIMESTAMP,
  codarr_output_fingerprint TEXT,
  codarr_output_size       INTEGER,
  codarr_output_mtime      INTEGER,
  codarr_output_full_hash  TEXT,     -- only when full_hash_enabled
  provenance         TEXT NOT NULL DEFAULT 'untouched',
                                    -- untouched | codarr_output
                                    -- | modified_since_transcode
  integrity_checked_at TIMESTAMP,
  status             TEXT NOT NULL, -- new|analyzed|queued|processing|done
                                    -- |failed|ignored|skipped|missing
  ignored            INTEGER NOT NULL DEFAULT 0,
  last_error         TEXT,
  created_at         TIMESTAMP NOT NULL,
  updated_at         TIMESTAMP NOT NULL
);
CREATE INDEX idx_media_status ON media_files(status);
CREATE INDEX idx_media_plan_kind ON media_files(plan_kind);
CREATE INDEX idx_media_video_codec ON media_files(video_codec);
CREATE INDEX idx_media_instance ON media_files(arr_instance_id);
CREATE INDEX idx_media_provenance ON media_files(provenance);

CREATE TABLE jobs (
  id                 INTEGER PRIMARY KEY,
  media_file_id      INTEGER NOT NULL REFERENCES media_files(id),
  kind               TEXT NOT NULL,
  origin             TEXT NOT NULL,  -- ingest | manual | recheck | space_sweep
  priority           INTEGER NOT NULL DEFAULT 100,
  state              TEXT NOT NULL,
  attempt            INTEGER NOT NULL DEFAULT 0,

  transform_json     TEXT NOT NULL,  -- the history record, see 17.2

  staging_path       TEXT,
  used_temp_dir      INTEGER NOT NULL DEFAULT 0,
  ffmpeg_argv        TEXT,
  probe_result       TEXT,
  progress_pct       REAL,
  progress_speed     REAL,
  estimated_seconds  INTEGER,
  actual_seconds     INTEGER,
  encoder_used       TEXT,
  decode_path        TEXT,           -- hardware | software
  fell_back          INTEGER NOT NULL DEFAULT 0,
  fallback_reason    TEXT,
  source_size        INTEGER,
  output_size        INTEGER,
  output_fingerprint TEXT,           -- recorded at promotion, section 12
  output_full_hash   TEXT,           -- only when full_hash_enabled
  blocked_by         TEXT,
  failure_code       TEXT,           -- 19.1, NOT NULL whenever state='failed'
  failure_message    TEXT,           -- always populated on failure
  stderr_tail        TEXT,
  queued_at          TIMESTAMP NOT NULL,
  started_at         TIMESTAMP,
  finished_at        TIMESTAMP
);
CREATE INDEX idx_jobs_state ON jobs(state, priority, queued_at);
CREATE INDEX idx_jobs_media ON jobs(media_file_id);
-- One active job per file: enqueue is idempotent.
CREATE UNIQUE INDEX idx_jobs_one_active_per_file ON jobs(media_file_id)
  WHERE state IN ('queued','running','verifying',
                  'awaiting_stream_end','promoting');

CREATE TABLE hw_capabilities (
  id             INTEGER PRIMARY KEY,
  backend        TEXT NOT NULL,
  codec          TEXT NOT NULL,
  profile        TEXT NOT NULL,
  direction      TEXT NOT NULL,   -- encode | decode
  works          INTEGER NOT NULL,
  error          TEXT,
  ffmpeg_version TEXT,
  probed_at      TIMESTAMP NOT NULL
);

CREATE TABLE throughput_stats (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  encoder    TEXT,
  resolution TEXT,
  samples    INTEGER NOT NULL,
  avg_value  REAL NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE events (
  id            INTEGER PRIMARY KEY,
  level         TEXT NOT NULL,
  category      TEXT NOT NULL,
  message       TEXT NOT NULL,
  media_file_id INTEGER,
  job_id        INTEGER,
  created_at    TIMESTAMP NOT NULL
);
