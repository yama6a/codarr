-- +migrate Up
--
-- The frames per second ffmpeg is currently writing, from the `fps` key of
-- -progress pipe:1 (14.3). 18.1 asks for it on the dashboard's current-job card
-- and the parser has always read it; nothing carried it to the UI.
--
-- It is written on the same throttled five-second flush as progress_pct and
-- progress_speed, never on its own, so it costs no extra write.
ALTER TABLE jobs ADD COLUMN progress_fps REAL;
