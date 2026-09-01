package ingest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/ingest/mock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/store"
)

const webhookID = "9f2c1d8e"

// radarrYama is VERIFY.md's live shape: the instance reports /media and only
// its own mapping turns that into a path Codarr can see.
func radarrYama() domain.ArrInstance {
	return domain.ArrInstance{
		ID:        10,
		Name:      "radarr-yama",
		Flavour:   domain.FlavourRadarr,
		WebhookID: webhookID,
		Enabled:   true,
	}
}

type webhookState struct {
	instance domain.ArrInstance
	mappings []domain.PathMapping
	rows     map[string]domain.MediaFile
	missing  []int64
}

func webhookStore(state *webhookState) *mock.WebhookStoreMock {
	return &mock.WebhookStoreMock{
		GetArrInstanceByWebhookIDFunc: func(_ context.Context, id string) (domain.ArrInstance, error) {
			if id != state.instance.WebhookID {
				return domain.ArrInstance{}, store.ErrNotFound
			}

			return state.instance, nil
		},
		ListArrPathMappingsFunc: func(context.Context, int64) ([]domain.PathMapping, error) {
			return state.mappings, nil
		},
		ListRootsFunc:   func(context.Context) ([]domain.Root, error) { return roots(), nil },
		GetSettingsFunc: func(context.Context) (domain.Settings, error) { return settings(), nil },
		GetMediaFileByPathFunc: func(_ context.Context, p string) (domain.MediaFile, error) {
			row, ok := state.rows[p]
			if !ok {
				return domain.MediaFile{}, store.ErrNotFound
			}

			return row, nil
		},
		MarkMediaMissingFunc: func(_ context.Context, ids []int64) (int64, error) {
			state.missing = append(state.missing, ids...)

			return int64(len(ids)), nil
		},
	}
}

func newWebhook(t *testing.T, state *webhookState, an ingest.FileAnalyzer, fsm ingest.FS) *ingest.Webhook {
	t.Helper()

	return ingest.NewWebhook(webhookStore(state), an, fsm, discardLogger())
}

func mediaMapping() []domain.PathMapping {
	return []domain.PathMapping{{ID: 1, Remote: "/media", Local: moviesRoot}}
}

// All four instances report /media (VERIFY.md), so remapping through the sending
// instance's own mappings is mandatory rather than optional.
func TestWebhook_HandleRemapsDownloadPathsThroughTheSendingInstance(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}
	an, seen := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:     ingest.EventDownload,
			EntityID: instanceID(4242),
			Title:    "Heat",
			Paths:    []string{"/media/Heat (1995)/Heat (1995).mkv"},
		})
	require.NoError(t, err)

	require.Equal(t, []string{moviesRoot + "/Heat (1995)/Heat (1995).mkv"}, *seen)
	require.True(t, ack.Received)
	require.Equal(t, "radarr-yama", ack.Instance)
	require.Equal(t, "radarr-yama: analysed 1 of 1 files", ack.Message)
	require.Len(t, ack.Results, 1)

	require.Equal(t, instanceID(4242), an.AnalyzeInCalls()[0].Env.ArrEntityID)
	require.Equal(t, domain.OriginIngest, an.AnalyzeInCalls()[0].Env.Origin)
}

func TestWebhook_HandleWalksTheFolderWhenARenameCarriesNoFilePaths(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}
	an, seen := recordingAnalyzer()

	tr := newTree(moviesRoot).
		add(moviesRoot+"/Heat (1995)/Heat (1995).mkv", big, now).
		add(moviesRoot+"/Heat (1995)/Heat (1995)-trailer.mkv", big, now).
		add(moviesRoot+"/Heat (1995)/Featurettes/Making Of.mkv", big, now).
		add(moviesRoot+"/Other Film/Other Film.mkv", big, now)

	_, err := newWebhook(t, state, an, tr.fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:       ingest.EventRename,
			FolderPath: "/media/Heat (1995)",
		})
	require.NoError(t, err)

	require.Equal(t, []string{moviesRoot + "/Heat (1995)/Heat (1995).mkv"}, *seen)
}

func TestWebhook_HandleMarksDeletedFilesMissing(t *testing.T) {
	t.Parallel()

	local := moviesRoot + "/Heat (1995)/Heat (1995).mkv"
	state := &webhookState{
		instance: radarrYama(),
		mappings: mediaMapping(),
		rows:     map[string]domain.MediaFile{local: {ID: 33}},
	}

	an, seen := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventMovieFileDelete,
			Paths: []string{"/media/Heat (1995)/Heat (1995).mkv", "/media/Unknown.mkv"},
		})
	require.NoError(t, err)

	require.Equal(t, []int64{33}, state.missing)
	require.Equal(t, []int64{33}, ack.MarkedMissing)
	require.Equal(t, "radarr-yama: marked 1 files missing", ack.Message)
	require.Empty(t, *seen, "a delete never analyses")
}

func TestWebhook_HandleMarksDeletedEpisodesMissing(t *testing.T) {
	t.Parallel()

	local := moviesRoot + "/Show/S01E01.mkv"
	state := &webhookState{
		instance: radarrYama(),
		mappings: mediaMapping(),
		rows:     map[string]domain.MediaFile{local: {ID: 44}},
	}

	an, _ := recordingAnalyzer()

	_, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventEpisodeFileDelete,
			Paths: []string{"/media/Show/S01E01.mkv"},
		})
	require.NoError(t, err)

	require.Equal(t, []int64{44}, state.missing)
}

// Test must return 200 with a body for the *arr's Test button (plan.md 13.1), even for
// an instance disabled in Codarr, because the operator is pasting the URL in right then.
func TestWebhook_HandleAcknowledgesTest(t *testing.T) {
	t.Parallel()

	disabled := radarrYama()
	disabled.Enabled = false

	state := &webhookState{instance: disabled, mappings: mediaMapping()}
	an, seen := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{Type: ingest.EventTest})
	require.NoError(t, err)

	require.Equal(t, ingest.Ack{
		Received: true,
		Instance: "radarr-yama",
		Message:  "Codarr received the test from radarr-yama",
	}, ack)
	require.Empty(t, *seen)
}

func TestWebhook_HandleIgnoresEventsForADisabledInstance(t *testing.T) {
	t.Parallel()

	disabled := radarrYama()
	disabled.Enabled = false

	state := &webhookState{instance: disabled, mappings: mediaMapping()}
	an, seen := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"/media/Heat.mkv"},
		})
	require.NoError(t, err)

	require.True(t, ack.Received)
	require.Equal(t, "radarr-yama is disabled in Codarr, so the event was ignored", ack.Message)
	require.Empty(t, *seen)
}

func TestWebhook_HandleAcknowledgesAnEventTypeItDoesNotActON(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}
	an, seen := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{Type: "Grab"})
	require.NoError(t, err)

	require.True(t, ack.Received)
	require.Equal(t, "event type Grab is not one Codarr acts on", ack.Message)
	require.Empty(t, *seen)
}

func TestWebhook_HandleRejectsAnUnknownWebhookID(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}
	an, _ := recordingAnalyzer()

	_, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), "not-a-webhook", ingest.Event{Type: ingest.EventDownload})
	require.ErrorIs(t, err, ingest.ErrUnknownWebhook)
}

func TestWebhook_HandleSurfacesALookupFailure(t *testing.T) {
	t.Parallel()

	st := webhookStore(&webhookState{instance: radarrYama()})
	st.GetArrInstanceByWebhookIDFunc = func(context.Context, string) (domain.ArrInstance, error) {
		return domain.ArrInstance{}, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := ingest.NewWebhook(st, an, newTree(moviesRoot).fs(), discardLogger()).
		Handle(t.Context(), webhookID, ingest.Event{Type: ingest.EventDownload})
	require.ErrorContains(t, err, "database is locked")
}

func TestWebhook_HandleReportsAnEventWithNothingUsableInIt(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}
	an, _ := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{Type: ingest.EventDownload})
	require.NoError(t, err)

	require.Equal(t,
		"no file paths in the payload that map into Codarr's view of the filesystem", ack.Message)
}

// An unmapped path lands outside every root and analysis refuses it (VERIFY.md), but the
// webhook still acknowledges, because failing it makes the *arr retry forever.
func TestWebhook_HandleKeepsGoingWhenOneFileFailsAnalysis(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}

	an := &mock.FileAnalyzerMock{
		AnalyzeInFunc: func(_ context.Context, p string, _ ingest.Env) (ingest.Result, error) {
			if p == moviesRoot+"/Bad.mkv" {
				return ingest.Result{}, errors.New("moov atom not found")
			}

			return ingest.Result{Path: p}, nil
		},
	}

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"/media/Bad.mkv", "/media/Good.mkv"},
		})
	require.NoError(t, err)

	require.Len(t, ack.Results, 1)
	require.Equal(t, "radarr-yama: analysed 1 of 2 files", ack.Message)
}

func TestWebhook_HandleSurfacesAMappingLookupFailure(t *testing.T) {
	t.Parallel()

	st := webhookStore(&webhookState{instance: radarrYama()})
	st.ListArrPathMappingsFunc = func(context.Context, int64) ([]domain.PathMapping, error) {
		return nil, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := ingest.NewWebhook(st, an, newTree(moviesRoot).fs(), discardLogger()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"/media/Heat.mkv"},
		})
	require.ErrorContains(t, err, "list path mappings for radarr-yama: database is locked")
}

func TestWebhook_HandleDropsAPathThatIsNotAbsolute(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama(), mappings: mediaMapping()}
	an, seen := recordingAnalyzer()

	ack, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"", "relative/path.mkv"},
		})
	require.NoError(t, err)

	require.Empty(t, *seen)
	require.Equal(t,
		"no file paths in the payload that map into Codarr's view of the filesystem", ack.Message)
}

func TestWebhook_HandleSurfacesAMarkMissingFailure(t *testing.T) {
	t.Parallel()

	local := moviesRoot + "/Heat.mkv"
	state := &webhookState{
		instance: radarrYama(),
		mappings: mediaMapping(),
		rows:     map[string]domain.MediaFile{local: {ID: 33}},
	}

	st := webhookStore(state)
	st.MarkMediaMissingFunc = func(context.Context, []int64) (int64, error) {
		return 0, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := ingest.NewWebhook(st, an, newTree(moviesRoot).fs(), discardLogger()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventMovieFileDelete,
			Paths: []string{"/media/Heat.mkv"},
		})
	require.ErrorContains(t, err, "mark 1 files missing")
}

func TestWebhook_HandleSurfacesARowLookupFailureOnDelete(t *testing.T) {
	t.Parallel()

	st := webhookStore(&webhookState{instance: radarrYama(), mappings: mediaMapping()})
	st.GetMediaFileByPathFunc = func(context.Context, string) (domain.MediaFile, error) {
		return domain.MediaFile{}, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := ingest.NewWebhook(st, an, newTree(moviesRoot).fs(), discardLogger()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventMovieFileDelete,
			Paths: []string{"/media/Heat.mkv"},
		})
	require.ErrorContains(t, err, "database is locked")
}

// An instance with no mapping is VERIFY.md's failure mode: the reported /media
// goes through unchanged, so nothing lands under a root.
func TestWebhook_HandleProcessesAnUnmappedPathAnyway(t *testing.T) {
	t.Parallel()

	state := &webhookState{instance: radarrYama()}
	an, seen := recordingAnalyzer()

	_, err := newWebhook(t, state, an, newTree(moviesRoot).fs()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"/media/Heat (1995)/Heat (1995).mkv"},
		})
	require.NoError(t, err)

	require.Equal(t, []string{"/media/Heat (1995)/Heat (1995).mkv"}, *seen,
		"the unmapped path is passed through, and analysis is what refuses it")
}

func TestWebhook_HandleSurfacesARootsFailure(t *testing.T) {
	t.Parallel()

	st := webhookStore(&webhookState{instance: radarrYama(), mappings: mediaMapping()})
	st.ListRootsFunc = func(context.Context) ([]domain.Root, error) {
		return nil, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := ingest.NewWebhook(st, an, newTree(moviesRoot).fs(), discardLogger()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"/media/Heat.mkv"},
		})
	require.ErrorContains(t, err, "list roots: database is locked")
}

func TestWebhook_HandleSurfacesASettingsFailure(t *testing.T) {
	t.Parallel()

	st := webhookStore(&webhookState{instance: radarrYama(), mappings: mediaMapping()})
	st.GetSettingsFunc = func(context.Context) (domain.Settings, error) {
		return domain.Settings{}, errors.New("database is locked")
	}

	an, _ := recordingAnalyzer()

	_, err := ingest.NewWebhook(st, an, newTree(moviesRoot).fs(), discardLogger()).
		Handle(t.Context(), webhookID, ingest.Event{
			Type:  ingest.EventDownload,
			Paths: []string{"/media/Heat.mkv"},
		})
	require.ErrorContains(t, err, "get settings: database is locked")
}
