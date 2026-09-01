package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

func testTime() time.Time {
	return time.Date(2026, 2, 14, 3, 22, 11, 0, time.UTC)
}

func seedMedia(t *testing.T, s store.Store, path string) domain.MediaFile {
	t.Helper()

	m, err := s.UpsertMediaFile(t.Context(), domain.MediaFile{
		Path:            path,
		SizeBytes:       9_871_234_567,
		MTime:           1_735_689_600,
		NLink:           1,
		Fingerprint:     "xxh3-128:source",
		FingerprintAlgo: "xxh3-128",
		Status:          domain.MediaNew,
		CreatedAt:       testTime(),
		UpdatedAt:       testTime(),
	})
	require.NoError(t, err)

	return m
}

func seedJob(t *testing.T, s store.Store, mediaFileID int64, kind domain.Kind, priority int) domain.Job {
	t.Helper()

	job, created, err := s.EnqueueJob(t.Context(), domain.Job{
		MediaFileID: mediaFileID,
		Kind:        kind,
		Origin:      domain.OriginIngest,
		Priority:    priority,
		QueuedAt:    testTime(),
		Transform: domain.TransformRecord{
			Container: domain.BeforeAfterString{Before: "matroska", After: "matroska"},
		},
	})
	require.NoError(t, err)
	require.True(t, created)

	return job
}

// forceJobState writes a state the public API will not produce, so the startup
// sweep can be tested against the wreckage a crash actually leaves behind.
func forceJobState(t *testing.T, db *store.DB, id int64, state domain.JobState, attempt int, staging string) {
	t.Helper()

	const query = `UPDATE jobs SET state = ?, attempt = ?, staging_path = ? WHERE id = ?`

	_, err := db.Writer().ExecContext(t.Context(), query, string(state), attempt, staging, id)
	require.NoError(t, err)
}
