package api

import (
	"context"
	"errors"
	"fmt"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// Provenance is derived from the fingerprints and never set by a user, so these
// endpoints recompute rather than accept (plan.md 12).

// VerifyMediaIntegrity recomputes one file's fingerprint and compares it with
// what was recorded at promotion.
func (s *Server) VerifyMediaIntegrity(
	ctx context.Context, req gen.VerifyMediaIntegrityRequestObject,
) (gen.VerifyMediaIntegrityResponseObject, error) {
	res, err := s.verifyOne(ctx, req.Id)
	if err != nil {
		return gen.VerifyMediaIntegritydefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.VerifyMediaIntegrity200JSONResponse(res), nil
}

// VerifyMediaIntegrityBulk is the same for a selection. One unreadable file does
// not fail the batch: its own result carries the message.
func (s *Server) VerifyMediaIntegrityBulk(
	ctx context.Context, req gen.VerifyMediaIntegrityBulkRequestObject,
) (gen.VerifyMediaIntegrityBulkResponseObject, error) {
	if req.Body == nil || len(req.Body.Ids) == 0 {
		return gen.VerifyMediaIntegrityBulk200JSONResponse{Results: []gen.IntegrityResult{}}, nil
	}

	if len(req.Body.Ids) > MaxSelectionSize {
		return gen.VerifyMediaIntegrityBulkdefaultJSONResponse(s.fail(ctx, badRequest(
			"at most %d ids", MaxSelectionSize))), nil
	}

	out := gen.IntegrityBulkResult{Results: make([]gen.IntegrityResult, 0, len(req.Body.Ids))}

	for _, id := range req.Body.Ids {
		res, err := s.verifyOne(ctx, id)
		if err != nil {
			// A bad request or a cancelled batch fails the whole call; a single
			// row that could not be loaded is reported in its own result.
			var apiErr *Error
			if errors.As(err, &apiErr) || ctx.Err() != nil {
				//nolint:nilerr // the failure is rendered into the response, not dropped
				return gen.VerifyMediaIntegrityBulkdefaultJSONResponse(s.fail(ctx, err)), nil
			}

			res = gen.IntegrityResult{
				CheckedAt:   s.clk.Now(),
				MediaFileId: id,
				Message:     ptrOf(err.Error()),
				Ok:          false,
			}
		}

		out.Checked++

		if !res.Ok {
			out.Mismatched++
		}

		out.Results = append(out.Results, res)
	}

	return gen.VerifyMediaIntegrityBulk200JSONResponse(out), nil
}

func (s *Server) verifyOne(ctx context.Context, id int64) (gen.IntegrityResult, error) {
	media, err := s.store.GetMediaFile(ctx, id)
	if err != nil {
		return gen.IntegrityResult{}, fmt.Errorf("get media file %d: %w", id, err)
	}

	if _, err := s.underRoots(ctx, media.Path); err != nil {
		return gen.IntegrityResult{}, err
	}

	now := s.clk.Now()
	res := gen.IntegrityResult{
		CheckedAt:           now,
		MediaFileId:         media.ID,
		Path:                media.Path,
		Provenance:          gen.Provenance(media.Provenance),
		RecordedFingerprint: strPtr(media.CodarrOutputFingerprint),
		RecordedFullHash:    strPtr(media.CodarrOutputFullHash),
		RecordedSizeBytes:   int64Ptr(media.CodarrOutputSize),
	}

	// A file that cannot be read is a result, not a request failure: the batch
	// form has to report the one bad file and carry on.
	info, err := s.fs.Stat(media.Path)
	if err != nil {
		res.Message = ptrOf("the file could not be stat'ed: " + err.Error())

		return res, nil //nolint:nilerr // an unreadable file is reported, not raised
	}

	res.CurrentSizeBytes = ptrOf(info.Size)

	current, err := s.fp.Sparse(media.Path)
	if err != nil {
		res.Message = ptrOf("the fingerprint could not be computed: " + err.Error())

		return res, nil //nolint:nilerr // an unreadable file is reported, not raised
	}

	res.CurrentFingerprint = ptrOf(current)

	// Only recomputed when one was recorded at promotion; a fresh one would have
	// nothing to compare against (plan.md 12.2).
	fullHash := ""

	if media.CodarrOutputFullHash != "" {
		fullHash, err = s.fp.Full(media.Path)
		if err != nil {
			res.Message = ptrOf("the whole-file hash could not be computed: " + err.Error())

			return res, nil //nolint:nilerr // an unreadable file is reported, not raised
		}

		res.CurrentFullHash = ptrOf(fullHash)
		res.FullHashChecked = true
	}

	provenance := domain.DeriveProvenance(media.CodarrOutputFingerprint, current)
	res.Provenance = gen.Provenance(provenance)
	res.Ok = provenance != domain.ProvenanceModified

	if res.FullHashChecked && fullHash != media.CodarrOutputFullHash {
		res.Ok = false
		res.Provenance = gen.ProvenanceModifiedSinceTranscode
	}

	res.Message = ptrOf(integrityMessage(res.Ok, provenance))

	if err := s.store.SetMediaIntegrity(ctx, media.ID, current, fullHash, now); err != nil {
		return gen.IntegrityResult{}, fmt.Errorf("record the integrity check for media file %d: %w", media.ID, err)
	}

	return res, nil
}

func integrityMessage(ok bool, p domain.Provenance) string {
	switch {
	case !ok:
		return "something rewrote this file after Codarr produced it"
	case p == domain.ProvenanceUntouched:
		return "Codarr has never written this file"
	default:
		return "the file still matches what Codarr recorded at promotion"
	}
}
