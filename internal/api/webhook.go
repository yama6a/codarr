package api

import (
	"context"
	"encoding/json"
	"fmt"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/ingest"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// ReceiveArrWebhook is the receiver for one instance's Connect webhook.
//
// A Test event must return 200 with a body, because that is the only thing the
// operator sees when they paste the URL and press Test; it succeeds even for an
// instance not yet enabled in Codarr.
func (s *Server) ReceiveArrWebhook(
	ctx context.Context, req gen.ReceiveArrWebhookRequestObject,
) (gen.ReceiveArrWebhookResponseObject, error) {
	if req.Body == nil {
		return gen.ReceiveArrWebhookdefaultJSONResponse(s.fail(ctx, badRequest("an event body is required"))), nil
	}

	instance, err := s.store.GetArrInstanceByWebhookID(ctx, req.WebhookId)
	if err != nil {
		return gen.ReceiveArrWebhookdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	ev, err := parseWebhook(instance.Flavour, *req.Body)
	if err != nil {
		return gen.ReceiveArrWebhookdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	ack, err := s.webhooks.Handle(ctx, req.WebhookId, ev)
	if err != nil {
		return gen.ReceiveArrWebhookdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.ReceiveArrWebhook200JSONResponse(webhookAck(ack)), nil
}

// parseWebhook hands the payload back to internal/arr, which owns the Radarr and
// Sonarr differences including the Rename shape that plan.md 13.1 gets wrong.
// The strict server has already decoded the body, so it is re-marshalled: the
// generated type keeps every unknown field in AdditionalProperties, so the round
// trip is lossless and renamedMovieFiles survives it.
func parseWebhook(flavour domain.Flavour, payload gen.ArrWebhookPayload) (ingest.Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ingest.Event{}, fmt.Errorf("%w: %w", arr.ErrBadPayload, err)
	}

	parsed, err := arr.ParseWebhook(flavour, raw)
	if err != nil {
		return ingest.Event{}, fmt.Errorf("parse %s webhook: %w", flavour, err)
	}

	ev := ingest.Event{
		Type:       ingest.EventType(parsed.Type),
		EntityID:   entityID(parsed),
		Title:      parsed.Title,
		FolderPath: folderPath(payload),
		IsUpgrade:  parsed.IsUpgrade,
	}

	for _, f := range parsed.Files {
		if f.RemotePath != "" {
			ev.Paths = append(ev.Paths, f.RemotePath)
		}

		// A Rename names both sides. The old path has to be retired too, or the
		// stored row keeps pointing at a file that no longer exists (13.1).
		if f.PreviousRemotePath != "" {
			ev.Paths = append(ev.Paths, f.PreviousRemotePath)
		}
	}

	return ev, nil
}

func entityID(e arr.Event) *int64 {
	switch {
	case e.Item.MovieID != 0:
		return ptrOf(e.Item.MovieID)
	case e.Item.SeriesID != 0:
		return ptrOf(e.Item.SeriesID)
	default:
		return nil
	}
}

// folderPath is read off the payload rather than the parsed event: arr.Event
// carries the folder only as a fallback for building file paths, and a Rename
// with no per-file paths needs the folder itself so ingest can walk it.
func folderPath(p gen.ArrWebhookPayload) string {
	if p.Movie != nil && p.Movie.FolderPath != nil {
		return *p.Movie.FolderPath
	}

	if p.Series != nil && p.Series.Path != nil {
		return *p.Series.Path
	}

	return ""
}

func webhookAck(a ingest.Ack) gen.WebhookAck {
	out := gen.WebhookAck{Message: a.Message, Received: a.Received}

	for _, r := range a.Results {
		if r.MediaFileID != 0 {
			out.MediaFileId = ptrOf(r.MediaFileID)
		}

		if r.JobID != 0 {
			out.JobId = ptrOf(r.JobID)
		}
	}

	return out
}
