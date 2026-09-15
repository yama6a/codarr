package store_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/pkg/store/storetest"
)

const (
	migrationsBeforeLabels = 5
	migrationsBeforePurge  = 6
)

func TestMigration006_RewritesKindsFromTheStoredPlans(t *testing.T) {
	t.Parallel()

	db := storetest.NewDBAt(t, migrationsBeforeLabels)
	w := db.Writer()

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := w.ExecContext(t.Context(), q, args...)
		require.NoError(t, err)
	}

	const media = `INSERT INTO media_files (id, path, size_bytes, mtime, status, plan_json, plan_kind, plan_reasons, created_at, updated_at)
		VALUES (?, ?, 1, 1, 'analyzed', ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`

	exec(media, 1, "/m/subs.mkv",
		`{"kind":"audio_only","source_container":"matroska","output_container":"matroska","level_rewrite":false,`+
			`"streams":[{"type":"video","decision":"copy"},{"type":"audio","decision":"copy"},{"type":"subtitle","decision":"drop"}]}`,
		"audio_only", `["video: COPY","plan: AUDIO_ONLY - video copied, 1 subtitle stream dropped"]`)
	exec(media, 2, "/m/full.avi",
		`{"kind":"full","source_container":"avi","output_container":"matroska","level_rewrite":false,`+
			`"streams":[{"type":"video","decision":"encode"},{"type":"audio","decision":"encode"}]}`,
		"full", `["plan: FULL - video re-encoded"]`)
	exec(media, 3, "/m/level.mkv",
		`{"kind":"remux","source_container":"matroska","output_container":"matroska","level_rewrite":true,`+
			`"streams":[{"type":"video","decision":"copy"},{"type":"audio","decision":"copy"}]}`,
		"remux", `["plan: REMUX - video copied with a level flag rewrite"]`)
	exec(media, 4, "/m/skip.mkv",
		`{"kind":"skip","source_container":"matroska","output_container":"matroska","level_rewrite":false,`+
			`"streams":[{"type":"video","decision":"copy"},{"type":"audio","decision":"copy"}]}`,
		"skip", `["plan: SKIP - every stream is already compatible"]`)
	exec(`INSERT INTO media_files (id, path, size_bytes, mtime, status, created_at, updated_at)
		VALUES (5, '/m/new.mkv', 1, 1, 'new', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)

	const job = `INSERT INTO jobs (id, media_file_id, kind, origin, priority, state, attempt, transform_json, queued_at)
		VALUES (?, ?, ?, 'ingest', 100, 'done', 1, ?, '2026-01-01T00:00:00Z')`

	exec(job, 1, 1, "audio_only",
		`{"container":{"before":"matroska","after":"matroska"},"video":{"action":"copy","before":{"level":"4.0"},"after":{"level":"4.0"}},`+
			`"audio":[{"action":"copy"}],"subtitles":[{"action":"drop"}]}`)
	exec(job, 2, 2, "full",
		`{"container":{"before":"avi","after":"matroska"},"video":{"action":"encode"},"audio":[{"action":"encode"}],"subtitles":[]}`)
	exec(job, 3, 3, "remux",
		`{"container":{"before":"matroska","after":"matroska"},"video":{"action":"copy","before":{"level":"5.1"},"after":{"level":"4.1"}},`+
			`"audio":[{"action":"copy"}],"subtitles":[]}`)
	exec(`INSERT INTO jobs (id, media_file_id, kind, origin, priority, state, attempt, transform_json, queued_at)
		VALUES (4, 4, 'full', 'ingest', 100, 'failed', 1, 'not json', '2026-01-01T00:00:00Z')`)

	const stat = `INSERT INTO throughput_stats (kind, encoder, resolution, samples, avg_value, updated_at)
		VALUES (?, ?, ?, ?, ?, '2026-01-01T00:00:00Z')`

	exec(stat, "audio_only", nil, nil, 3, 100.0)
	exec(stat, "remux", nil, nil, 1, 300.0)
	exec(stat, "full", "hevc_qsv", "1080p", 2, 5.0)

	require.NoError(t, store.MigrateMax(db, storetest.Logger(), 1))

	rows, err := w.QueryContext(t.Context(),
		`SELECT id, plan_kind, json_extract(plan_json, '$.kind'), json_extract(plan_reasons, '$[#-1]') FROM media_files ORDER BY id`)
	require.NoError(t, err)

	defer func() { require.NoError(t, rows.Close()) }()

	type mediaRow struct {
		id       int64
		kind     *string
		jsonKind *string
		planLine *string
	}

	var got []mediaRow

	for rows.Next() {
		var r mediaRow
		require.NoError(t, rows.Scan(&r.id, &r.kind, &r.jsonKind, &r.planLine))
		got = append(got, r)
	}

	require.NoError(t, rows.Err())
	require.Equal(t, []mediaRow{
		{1, ptr("subtitles"), ptr("subtitles"), ptr("plan: SUBTITLES - video copied, 1 subtitle stream dropped")},
		{2, ptr("video|audio|remux"), ptr("video|audio|remux"), ptr("plan: VIDEO|AUDIO|REMUX - video re-encoded")},
		{3, ptr("remux"), ptr("remux"), ptr("plan: REMUX - video copied with a level flag rewrite")},
		{4, ptr(""), ptr(""), ptr("plan: SKIP - every stream is already compatible")},
		{5, nil, nil, nil},
	}, got)

	var kinds []string

	jobRows, err := w.QueryContext(t.Context(), `SELECT kind FROM jobs ORDER BY id`)
	require.NoError(t, err)

	defer func() { require.NoError(t, jobRows.Close()) }()

	for jobRows.Next() {
		var k string
		require.NoError(t, jobRows.Scan(&k))
		kinds = append(kinds, k)
	}

	require.NoError(t, jobRows.Err())
	require.Equal(t, []string{"subtitles", "video|audio|remux", "remux", "video"}, kinds)

	s := storetest.NewStore(t, db)

	stats, err := s.ListThroughputStats(t.Context())
	require.NoError(t, err)
	require.Len(t, stats, 2)
	require.Equal(t, "io", string(stats[0].Kind))
	require.Equal(t, 4, stats[0].Samples)
	require.InEpsilon(t, 150.0, stats[0].AvgValue, 0.0001)
	require.Equal(t, "video", string(stats[1].Kind))
	require.Equal(t, "hevc_qsv", stats[1].Encoder)
}

func ptr(s string) *string { return &s }

func TestMigration007_PurgesFailedJobsAndOrphanedMedia(t *testing.T) {
	t.Parallel()

	db := storetest.NewDBAt(t, migrationsBeforePurge)
	w := db.Writer()

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := w.ExecContext(t.Context(), q, args...)
		require.NoError(t, err)
	}

	const media = `INSERT INTO media_files (id, path, size_bytes, mtime, status, plan_json, plan_kind, codarr_output_fingerprint, last_error, created_at, updated_at)
		VALUES (?, ?, 1, 1, ?, ?, ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`

	exec(media, 1, "/m/only-failed.mkv", "failed", nil, nil, nil, "boom")
	exec(media, 2, "/m/failed-then-done.mkv", "failed", `{"kind":"audio"}`, "audio", "xxh3:abc", "boom")
	exec(media, 3, "/m/failed-then-queued.mkv", "failed", `{"kind":""}`, "", nil, "boom")
	exec(media, 4, "/m/probe-failed.mkv", "failed", nil, nil, nil, "ffprobe exploded")
	exec(media, 5, "/m/untouched.mkv", "analyzed", `{"kind":"audio"}`, "audio", nil, nil)

	const job = `INSERT INTO jobs (id, media_file_id, kind, origin, priority, state, attempt, transform_json, queued_at)
		VALUES (?, ?, 'audio', 'ingest', 100, ?, 1, '{}', '2026-01-01T00:00:00Z')`

	exec(job, 1, 1, "failed")
	exec(job, 2, 2, "failed")
	exec(job, 3, 2, "done")
	exec(job, 4, 3, "failed")
	exec(job, 5, 3, "queued")
	exec(job, 6, 5, "done")

	const event = `INSERT INTO events (id, level, category, message, media_file_id, job_id, created_at)
		VALUES (?, 'info', 'job', 'x', ?, ?, '2026-01-01T00:00:00Z')`

	exec(event, 1, 1, 1)
	exec(event, 2, 1, nil)
	exec(event, 3, 2, 2)
	exec(event, 4, 2, 3)
	exec(event, 5, 5, 6)
	exec(event, 6, nil, nil)

	require.NoError(t, store.Migrate(db, storetest.Logger()))

	ids := func(q string) []int64 {
		t.Helper()

		rows, err := w.QueryContext(t.Context(), q)
		require.NoError(t, err)

		defer func() { require.NoError(t, rows.Close()) }()

		var out []int64

		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}

		require.NoError(t, rows.Err())

		return out
	}

	require.Equal(t, []int64{3, 5, 6}, ids(`SELECT id FROM jobs ORDER BY id`))
	require.Equal(t, []int64{2, 3, 4, 5}, ids(`SELECT id FROM media_files ORDER BY id`))
	require.Equal(t, []int64{4, 5, 6}, ids(`SELECT id FROM events ORDER BY id`))

	rows, err := w.QueryContext(t.Context(), `SELECT id, status, last_error FROM media_files ORDER BY id`)
	require.NoError(t, err)

	defer func() { require.NoError(t, rows.Close()) }()

	type row struct {
		id      int64
		status  string
		lastErr *string
	}

	var got []row

	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.id, &r.status, &r.lastErr))
		got = append(got, r)
	}

	require.NoError(t, rows.Err())
	require.Equal(t, []row{
		{2, "done", nil},
		{3, "skipped", nil},
		{4, "failed", ptr("ffprobe exploded")},
		{5, "analyzed", nil},
	}, got)
}
