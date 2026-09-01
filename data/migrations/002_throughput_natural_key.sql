-- +migrate Up
--
-- (kind, encoder, resolution) is throughput_stats' natural key (14.3), unconstrained in
-- 001 so the upsert leaned on the single write connection. COALESCE because encoder and
-- resolution are NULL for audio_only and remux, and NULLs never compare equal.
CREATE UNIQUE INDEX IF NOT EXISTS idx_throughput_natural_key
  ON throughput_stats(kind, COALESCE(encoder, ''), COALESCE(resolution, ''));
