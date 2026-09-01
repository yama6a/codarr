-- +migrate Up
--
-- The fps key of -progress pipe:1 (14.3), for the dashboard card of 18.1. Written on
-- the same throttled five-second flush as progress_pct, never on its own.
ALTER TABLE jobs ADD COLUMN progress_fps REAL;
