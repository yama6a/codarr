-- +migrate Up
--
-- plan.md 7: the plan kind is the set of labels a plan carries, pipe-joined in
-- fixed order, '' when nothing needs doing. Derived from the stored JSON so the
-- rewrite needs no probe.

UPDATE media_files SET plan_kind = rtrim(
    CASE WHEN EXISTS (SELECT 1 FROM json_each(media_files.plan_json, '$.streams') s
                      WHERE json_extract(s.value, '$.type') = 'video'
                        AND json_extract(s.value, '$.decision') = 'encode')
         THEN 'video|' ELSE '' END ||
    CASE WHEN EXISTS (SELECT 1 FROM json_each(media_files.plan_json, '$.streams') s
                      WHERE json_extract(s.value, '$.type') = 'audio'
                        AND json_extract(s.value, '$.decision') <> 'copy')
         THEN 'audio|' ELSE '' END ||
    CASE WHEN EXISTS (SELECT 1 FROM json_each(media_files.plan_json, '$.streams') s
                      WHERE json_extract(s.value, '$.type') = 'subtitle'
                        AND json_extract(s.value, '$.decision') <> 'copy')
         THEN 'subtitles|' ELSE '' END ||
    CASE WHEN json_extract(plan_json, '$.level_rewrite') = 1
           OR json_extract(plan_json, '$.source_container') <> json_extract(plan_json, '$.output_container')
         THEN 'remux|' ELSE '' END, '|')
WHERE plan_json IS NOT NULL AND json_valid(plan_json);

UPDATE media_files SET plan_kind = NULL WHERE plan_json IS NULL;

UPDATE media_files SET plan_json = json_set(plan_json, '$.kind', plan_kind)
WHERE plan_json IS NOT NULL AND json_valid(plan_json);

UPDATE media_files SET plan_reasons = json_set(plan_reasons, '$[#-1]',
    'plan: ' || CASE WHEN plan_kind = '' THEN 'SKIP' ELSE upper(plan_kind) END
      || substr(json_extract(plan_reasons, '$[#-1]'),
                instr(json_extract(plan_reasons, '$[#-1]'), ' - ')))
WHERE plan_reasons IS NOT NULL AND json_valid(plan_reasons)
  AND json_array_length(plan_reasons) > 0
  AND json_extract(plan_reasons, '$[#-1]') LIKE 'plan: %'
  AND instr(json_extract(plan_reasons, '$[#-1]'), ' - ') > 0;

-- jobs.kind from the transform record (17.2). A level rewrite is a copy whose
-- after.level differs from before.level.
UPDATE jobs SET kind = rtrim(
    CASE WHEN json_extract(transform_json, '$.video.action') = 'encode' THEN 'video|' ELSE '' END ||
    CASE WHEN EXISTS (SELECT 1 FROM json_each(jobs.transform_json, '$.audio') a
                      WHERE json_extract(a.value, '$.action') <> 'copy') THEN 'audio|' ELSE '' END ||
    CASE WHEN EXISTS (SELECT 1 FROM json_each(jobs.transform_json, '$.subtitles') s
                      WHERE json_extract(s.value, '$.action') <> 'copy') THEN 'subtitles|' ELSE '' END ||
    CASE WHEN json_extract(transform_json, '$.container.before') <> json_extract(transform_json, '$.container.after')
           OR (json_extract(transform_json, '$.video.action') = 'copy'
               AND json_extract(transform_json, '$.video.before.level') <> json_extract(transform_json, '$.video.after.level'))
         THEN 'remux|' ELSE '' END, '|')
WHERE transform_json IS NOT NULL AND json_valid(transform_json);

UPDATE jobs SET kind = CASE kind WHEN 'full' THEN 'video' WHEN 'audio_only' THEN 'audio' WHEN 'skip' THEN '' ELSE kind END
WHERE kind IN ('full', 'audio_only', 'skip');

-- throughput_stats (14.3): every I/O bound plan shares one row, so the old
-- audio_only and remux averages merge weighted by their samples.
INSERT INTO throughput_stats (kind, encoder, resolution, samples, avg_value, updated_at)
SELECT 'io', NULL, NULL, min(SUM(samples), 20),
       SUM(samples * avg_value) / SUM(samples), MAX(updated_at)
FROM throughput_stats
WHERE kind IN ('audio_only', 'remux') AND samples > 0
GROUP BY 1;

DELETE FROM throughput_stats WHERE kind IN ('audio_only', 'remux');

UPDATE throughput_stats SET kind = 'video' WHERE kind = 'full';
