-- +migrate Up
--
-- One-off purge of the failed jobs recorded while the pipeline was being tested.
-- A media row goes with them only when no other job remains on it; the next
-- scan recreates anything still on disk.

CREATE TEMP TABLE purge_media AS
  SELECT DISTINCT media_file_id AS id FROM jobs WHERE state = 'failed';

DELETE FROM events WHERE job_id IN (SELECT id FROM jobs WHERE state = 'failed');

DELETE FROM jobs WHERE state = 'failed';

DELETE FROM events
 WHERE media_file_id IN (SELECT id FROM purge_media)
   AND media_file_id NOT IN (SELECT media_file_id FROM jobs);

DELETE FROM media_files
 WHERE id IN (SELECT id FROM purge_media)
   AND id NOT IN (SELECT media_file_id FROM jobs);

UPDATE media_files SET
  status = CASE
    WHEN codarr_output_fingerprint IS NOT NULL THEN 'done'
    WHEN plan_json IS NULL THEN 'new'
    WHEN plan_kind = '' THEN 'skipped'
    ELSE 'analyzed' END,
  last_error = NULL
 WHERE status = 'failed' AND id IN (SELECT id FROM purge_media);

DROP TABLE purge_media;
