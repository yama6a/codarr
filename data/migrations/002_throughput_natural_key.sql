-- +migrate Up
--
-- (kind, encoder, resolution) is throughput_stats' natural key: 14.3 keeps one
-- rolling average per job kind, and per (encoder, resolution) for full jobs.
-- 001 shipped no constraint, so an upsert had to be UPDATE-then-INSERT inside a
-- transaction. That is only safe because the store holds a single write
-- connection; the index makes it safe regardless.
--
-- COALESCE because encoder and resolution are NULL for audio_only and remux,
-- and NULLs do not compare equal in a unique index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_throughput_natural_key
  ON throughput_stats(kind, COALESCE(encoder, ''), COALESCE(resolution, ''));
