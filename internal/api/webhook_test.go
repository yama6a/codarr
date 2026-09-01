package api_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

// The receiver dispatches by webhook id, so nothing guesses which instance sent an
// event, and a Test answers 200 with a body, the operator's only feedback (plan.md 13.1).

func withWebhookInstance(h *harness, flavour domain.Flavour) {
	h.store.GetArrInstanceByWebhookIDFunc = func(_ context.Context, id string) (domain.ArrInstance, error) {
		if id != "hook-1" {
			return domain.ArrInstance{}, store.ErrNotFound
		}

		return domain.ArrInstance{ID: 1, Name: "radarr-4k", Flavour: flavour, Enabled: true}, nil
	}
}

func TestReceiveArrWebhook_TestEventAnswers200WithABody(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	withWebhookInstance(h, domain.FlavourRadarr)

	h.webhooks.HandleFunc = func(_ context.Context, id string, ev ingest.Event) (ingest.Ack, error) {
		require.Equal(t, "hook-1", id)
		require.Equal(t, ingest.EventTest, ev.Type)

		return ingest.Ack{Received: true, Message: "Codarr received the test from radarr-4k"}, nil
	}

	got := decodeInto[gen.WebhookAck](t, h.do(t, "POST", "/api/webhook/hook-1",
		map[string]any{"eventType": "Test"}), 200)

	require.True(t, got.Received)
	require.NotEmpty(t, got.Message)
}

func TestReceiveArrWebhook_DownloadCarriesThePathsAndEntityID(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	withWebhookInstance(h, domain.FlavourRadarr)

	var seen ingest.Event

	h.webhooks.HandleFunc = func(_ context.Context, _ string, ev ingest.Event) (ingest.Ack, error) {
		seen = ev

		return ingest.Ack{
			Received: true,
			Message:  "radarr-4k: analysed 1 of 1 files",
			Results:  []ingest.Result{{MediaFileID: 12, JobID: 34, Queued: true}},
		}, nil
	}

	got := decodeInto[gen.WebhookAck](t, h.do(t, "POST", "/api/webhook/hook-1", map[string]any{
		"eventType": "Download",
		"movie":     map[string]any{"id": 77, "title": "Dune", "folderPath": "/media/movies/Dune"},
		"movieFile": map[string]any{"id": 5, "relativePath": "Dune.mkv", "path": "/media/movies/Dune/Dune.mkv"},
	}), 200)

	require.True(t, got.Received)
	require.NotNil(t, got.MediaFileId)
	require.Equal(t, int64(12), *got.MediaFileId)
	require.NotNil(t, got.JobId)
	require.Equal(t, int64(34), *got.JobId)

	require.Equal(t, ingest.EventDownload, seen.Type)
	require.Equal(t, []string{"/media/movies/Dune/Dune.mkv"}, seen.Paths)
	require.Equal(t, "/media/movies/Dune", seen.FolderPath)
	require.NotNil(t, seen.EntityID)
	require.Equal(t, int64(77), *seen.EntityID)
}

// plan.md 13.1's field list is wrong for Rename: Radarr sends renamedMovieFiles with
// previousPath and no movieFile, and the unknown fields survive the strict decode.
func TestReceiveArrWebhook_RenameCarriesBothSidesOfThePath(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	withWebhookInstance(h, domain.FlavourRadarr)

	var seen ingest.Event

	h.webhooks.HandleFunc = func(_ context.Context, _ string, ev ingest.Event) (ingest.Ack, error) {
		seen = ev

		return ingest.Ack{Received: true, Message: "ok"}, nil
	}

	rec := h.do(t, "POST", "/api/webhook/hook-1", map[string]any{
		"eventType": "Rename",
		"movie":     map[string]any{"id": 77, "folderPath": "/media/movies/Dune"},
		"renamedMovieFiles": []map[string]any{{
			"id":                   5,
			"path":                 "/media/movies/Dune/Dune (2021).mkv",
			"previousPath":         "/media/movies/Dune/Dune.mkv",
			"relativePath":         "Dune (2021).mkv",
			"previousRelativePath": "Dune.mkv",
		}},
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, ingest.EventRename, seen.Type)
	require.Equal(t, []string{
		"/media/movies/Dune/Dune (2021).mkv",
		"/media/movies/Dune/Dune.mkv",
	}, seen.Paths)
}

func TestReceiveArrWebhook_SonarrSeriesPathIsTheFolder(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	withWebhookInstance(h, domain.FlavourSonarr)

	var seen ingest.Event

	h.webhooks.HandleFunc = func(_ context.Context, _ string, ev ingest.Event) (ingest.Ack, error) {
		seen = ev

		return ingest.Ack{Received: true, Message: "ok"}, nil
	}

	rec := h.do(t, "POST", "/api/webhook/hook-1", map[string]any{
		"eventType": "Download",
		"series":    map[string]any{"id": 9, "title": "Severance", "path": "/media/tv/Severance"},
		"episodeFile": map[string]any{
			"id": 3, "relativePath": "S01E01.mkv", "path": "/media/tv/Severance/S01E01.mkv",
		},
	})

	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "/media/tv/Severance", seen.FolderPath)
	require.NotNil(t, seen.EntityID)
	require.Equal(t, int64(9), *seen.EntityID)
}

func TestReceiveArrWebhook_UnknownWebhookIDIs404(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	withWebhookInstance(h, domain.FlavourRadarr)

	body := decodeInto[gen.Error](t, h.do(t, "POST", "/api/webhook/nope",
		map[string]any{"eventType": "Test"}), 404)

	require.Equal(t, "not_found", body.Error)
	require.Empty(t, h.webhooks.HandleCalls())
}
