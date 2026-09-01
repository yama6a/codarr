package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/pkg/store"
	"github.com/yama6a/codarr/internal/plex"
)

// GetPlex returns the single Plex server. The token is never in the response
// (plan.md 18.4).
func (s *Server) GetPlex(ctx context.Context, _ gen.GetPlexRequestObject) (gen.GetPlexResponseObject, error) {
	cfg, mappings, err := s.plexState(ctx)
	if err != nil {
		return gen.GetPlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetPlex200JSONResponse(plexConfig(cfg, mappings)), nil
}

// UpdatePlex replaces the configuration and its whole mapping list. The token is
// only overwritten when the submitted value is not the mask (18.4).
func (s *Server) UpdatePlex(
	ctx context.Context, req gen.UpdatePlexRequestObject,
) (gen.UpdatePlexResponseObject, error) {
	if req.Body == nil {
		return gen.UpdatePlexdefaultJSONResponse(s.fail(ctx, badRequest("a plex body is required"))), nil
	}

	cfg, _, err := s.plexState(ctx)
	if err != nil {
		return gen.UpdatePlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	mappings, err := domainMappings(req.Body.PathMappings)
	if err != nil {
		return gen.UpdatePlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	cfg.BaseURL = strings.TrimSpace(req.Body.BaseUrl)
	cfg.Token = keepOrReplace(cfg.Token, req.Body.Token)
	cfg.RefreshAfter = req.Body.RefreshAfter
	cfg.AnalyzeAfter = req.Body.AnalyzeAfter
	cfg.GuardActiveStreams = req.Body.GuardActiveStreams

	if err := s.store.UpdatePlexConfig(ctx, cfg); err != nil {
		return gen.UpdatePlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if err := s.store.ReplacePlexPathMappings(ctx, mappings); err != nil {
		return gen.UpdatePlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	stored, storedMappings, err := s.plexState(ctx)
	if err != nil {
		return gen.UpdatePlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.UpdatePlex200JSONResponse(plexConfig(stored, storedMappings)), nil
}

// TestPlex proves the token works and the libraries are visible; a reachable server
// rejecting the token is a successful test with ok false, not an error.
func (s *Server) TestPlex(ctx context.Context, _ gen.TestPlexRequestObject) (gen.TestPlexResponseObject, error) {
	client, err := s.plexClient(ctx)
	if err != nil {
		return gen.TestPlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	res := client.Test(ctx)
	now := s.clk.Now()

	if err := s.store.SetPlexTestResult(ctx, now, res.Message); err != nil {
		return gen.TestPlexdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.TestPlex200JSONResponse{
		Message:       res.Message,
		Ok:            res.OK,
		ServerName:    strPtr(res.ServerName),
		ServerVersion: strPtr(res.ServerVersion),
		TestedAt:      now,
	}, nil
}

// ListPlexLibraries is a live query of the server's sections.
func (s *Server) ListPlexLibraries(
	ctx context.Context, _ gen.ListPlexLibrariesRequestObject,
) (gen.ListPlexLibrariesResponseObject, error) {
	client, err := s.plexClient(ctx)
	if err != nil {
		return gen.ListPlexLibrariesdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	sections, err := client.Sections(ctx)
	if err != nil {
		return gen.ListPlexLibrariesdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out := make([]gen.PlexLibrary, 0, len(sections))
	for _, sec := range sections {
		out = append(out, gen.PlexLibrary{
			Key:       sec.Key,
			Locations: sec.Locations,
			Title:     sec.Title,
			Type:      sec.Type,
		})
	}

	return gen.ListPlexLibraries200JSONResponse(out), nil
}

// ResolvePlexPath is the resolver of plan.md 18.4, answering from the stored mappings
// rather than the server so it works before a token is configured.
func (s *Server) ResolvePlexPath(
	ctx context.Context, req gen.ResolvePlexPathRequestObject,
) (gen.ResolvePlexPathResponseObject, error) {
	if req.Body == nil {
		return gen.ResolvePlexPathdefaultJSONResponse(s.fail(ctx, badRequest("a path is required"))), nil
	}

	local, err := s.underRoots(ctx, req.Body.Path)
	if err != nil {
		return gen.ResolvePlexPathdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	mappings, err := s.store.ListPlexPathMappings(ctx)
	if err != nil {
		return gen.ResolvePlexPathdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	remote, matched := pathmap.New(mappings).ToRemote(local)

	return gen.ResolvePlexPath200JSONResponse{
		LocalPath:  local,
		MappingId:  matchedMappingID(mappings, local),
		Matched:    matched,
		RemotePath: remote,
	}, nil
}

// matchedMappingID names which rule fired, so the UI can highlight it. The rule
// is the longest local prefix, the same order pathmap applies.
func matchedMappingID(mappings []domain.PathMapping, local string) *int64 {
	var (
		best   *int64
		length int
	)

	for _, m := range mappings {
		prefix := pathmap.Normalise(m.Local)
		if prefix == "" || !pathmap.UnderPrefix(local, prefix) || len(prefix) <= length {
			continue
		}

		id := m.ID
		best, length = &id, len(prefix)
	}

	return best
}

// StartPlexAuth begins the PIN flow; the client identifier is persisted because plex.tv
// ties the token to it and a fresh one registers a new device (plan.md 16.1).
func (s *Server) StartPlexAuth(
	ctx context.Context, _ gen.StartPlexAuthRequestObject,
) (gen.StartPlexAuthResponseObject, error) {
	cfg, _, err := s.plexState(ctx)
	if err != nil {
		return gen.StartPlexAuthdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if cfg.ClientIdentifier == "" {
		id, genErr := plex.NewClientIdentifier()
		if genErr != nil {
			return gen.StartPlexAuthdefaultJSONResponse(s.fail(ctx, genErr)), nil
		}

		cfg.ClientIdentifier = id

		if err := s.store.UpdatePlexConfig(ctx, cfg); err != nil {
			return gen.StartPlexAuthdefaultJSONResponse(s.fail(ctx, err)), nil
		}
	}

	pin, err := s.plexAuth.CreatePin(ctx, cfg.ClientIdentifier)
	if err != nil {
		return gen.StartPlexAuthdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.StartPlexAuth200JSONResponse{
		AuthUrl:          s.plexAuth.AuthURL(cfg.ClientIdentifier, pin.Code),
		ClientIdentifier: cfg.ClientIdentifier,
		Code:             pin.Code,
		ExpiresAt:        pin.ExpiresAt,
		PinId:            pin.ID,
	}, nil
}

// PollPlexAuth stores the token once the PIN is claimed and never returns it, which
// would contradict 18.4.
func (s *Server) PollPlexAuth(
	ctx context.Context, req gen.PollPlexAuthRequestObject,
) (gen.PollPlexAuthResponseObject, error) {
	cfg, _, err := s.plexState(ctx)
	if err != nil {
		return gen.PollPlexAuthdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if cfg.ClientIdentifier == "" {
		return gen.PollPlexAuthdefaultJSONResponse(s.fail(ctx,
			conflict("no_pin_flow", "no PIN flow has been started"))), nil
	}

	pin, err := s.plexAuth.CheckPin(ctx, cfg.ClientIdentifier, req.PinId)
	if err != nil {
		return gen.PollPlexAuthdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if !pin.Authorized() {
		return gen.PollPlexAuth200JSONResponse{
			Authorized:  false,
			Message:     ptrOf("waiting for the PIN to be claimed at plex.tv"),
			TokenStored: false,
		}, nil
	}

	cfg.Token = pin.AuthToken
	if err := s.store.UpdatePlexConfig(ctx, cfg); err != nil {
		return gen.PollPlexAuthdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.PollPlexAuth200JSONResponse{
		Authorized:  true,
		Message:     ptrOf("token stored"),
		TokenStored: true,
	}, nil
}

// plexState tolerates a missing row: a fresh install has no Plex configured and
// the settings page has to render so the operator can add one (plan.md 21).
func (s *Server) plexState(ctx context.Context) (domain.PlexConfig, []domain.PathMapping, error) {
	cfg, err := s.store.GetPlexConfig(ctx)
	if errors.Is(err, store.ErrNotFound) {
		cfg = domain.PlexConfig{RefreshAfter: true, AnalyzeAfter: true, GuardActiveStreams: true}
	} else if err != nil {
		return domain.PlexConfig{}, nil, fmt.Errorf("read the plex configuration: %w", err)
	}

	mappings, err := s.store.ListPlexPathMappings(ctx)
	if err != nil {
		return domain.PlexConfig{}, nil, fmt.Errorf("list plex path mappings: %w", err)
	}

	return cfg, mappings, nil
}

func (s *Server) plexClient(ctx context.Context) (PlexClient, error) {
	if s.plex == nil {
		return nil, fmt.Errorf("%w: no plex client factory wired", plex.ErrNotConfigured)
	}

	return s.plex(ctx)
}
