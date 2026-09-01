package api

import (
	"context"
	"log/slog"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// ListRoots returns every watch root with its owning instance, and every root
// two enabled instances both claim. plan.md 18.4 wants the conflict shown as a
// standing error rather than only after an import, and it is the same rows, so
// it rides on this response instead of an endpoint of its own.
func (s *Server) ListRoots(ctx context.Context, _ gen.ListRootsRequestObject) (gen.ListRootsResponseObject, error) {
	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		return gen.ListRootsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	names, err := s.instanceNames(ctx)
	if err != nil {
		return gen.ListRootsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out := make([]gen.Root, 0, len(roots))
	for _, r := range roots {
		out = append(out, root(r, instanceName(names, r.ArrInstanceID), nil))
	}

	return gen.ListRoots200JSONResponse{Roots: out, Conflicts: contestedRoots(roots, names)}, nil
}

// contestedRoots is pathmap.Conflicts rendered for the settings page. Only
// enabled roots owned by an instance count, which is the same rule attribution
// uses (plan.md 16.2).
func contestedRoots(roots []domain.Root, names map[int64]string) []gen.ContestedRoot {
	conflicts := pathmap.Conflicts(roots)

	out := make([]gen.ContestedRoot, 0, len(conflicts))

	for _, c := range conflicts {
		instances := make([]gen.ArrInstanceRef, 0, len(c.InstanceIDs))
		for _, id := range c.InstanceIDs {
			instances = append(instances, gen.ArrInstanceRef{Id: id, Name: names[id]})
		}

		out = append(out, gen.ContestedRoot{Path: c.Path, Instances: instances})
	}

	return out
}

// CreateRoot adds a root by hand. A root that nests inside an existing one is
// refused: two roots covering the same file make attribution ambiguous, and
// plan.md 16.2 says never to guess an owner.
func (s *Server) CreateRoot(
	ctx context.Context, req gen.CreateRootRequestObject,
) (gen.CreateRootResponseObject, error) {
	if req.Body == nil {
		return gen.CreateRootdefaultJSONResponse(s.fail(ctx, badRequest("a root body is required"))), nil
	}

	path, err := s.rootPath(ctx, req.Body.Path)
	if err != nil {
		return gen.CreateRootdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if req.Body.ArrInstanceId != nil {
		if _, err := s.store.GetArrInstance(ctx, *req.Body.ArrInstanceId); err != nil {
			return gen.CreateRootdefaultJSONResponse(s.fail(ctx, err)), nil
		}
	}

	created, err := s.store.CreateRoot(ctx, domain.Root{
		Path:          path,
		ArrInstanceID: req.Body.ArrInstanceId,
		Enabled:       boolOr(req.Body.Enabled, true),
	})
	if err != nil {
		return gen.CreateRootdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	names, err := s.instanceNames(ctx)
	if err != nil {
		return gen.CreateRootdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.CreateRoot201JSONResponse(root(created, instanceName(names, created.ArrInstanceID), nil)), nil
}

// DeleteRoot removes a root. The media rows under it are kept: they carry the
// job history, and plan.md 13.2 retires files by status rather than deletion.
func (s *Server) DeleteRoot(
	ctx context.Context, req gen.DeleteRootRequestObject,
) (gen.DeleteRootResponseObject, error) {
	if err := s.store.DeleteRoot(ctx, req.Id); err != nil {
		return gen.DeleteRootdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.DeleteRoot204Response{}, nil
}

// ScanRoot walks one root now. The walk runs in the background and the response
// is 202: a full root takes minutes and the request would time out long before
// it finished.
func (s *Server) ScanRoot(ctx context.Context, req gen.ScanRootRequestObject) (gen.ScanRootResponseObject, error) {
	if _, err := s.store.GetRoot(ctx, req.Id); err != nil {
		return gen.ScanRootdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	started := s.clk.Now()

	// Detaching is the point: the request context is cancelled the moment the
	// 202 is written, and a full root walk takes minutes.
	//nolint:gosec,contextcheck // G118: the request context must not bound the walk
	go s.scanInBackground(req.Id)

	return gen.ScanRoot202JSONResponse{RootId: req.Id, StartedAt: started}, nil
}

// scanInBackground detaches from the request context, which is cancelled the
// moment the 202 is written.
func (s *Server) scanInBackground(rootID int64) {
	ctx := context.Background()

	report, err := s.scanner.ScanRoot(ctx, rootID)
	if err != nil {
		s.log.ErrorContext(ctx, "manual root scan failed",
			slog.Int64("root_id", rootID), slog.String("error", err.Error()))

		return
	}

	s.log.InfoContext(ctx, "manual root scan finished",
		slog.Int64("root_id", rootID),
		slog.Int("walked", report.Walked),
		slog.Int("analyzed", report.Analyzed),
		slog.Int("queued", report.Queued),
		slog.Int("missing", report.Missing))
}

func instanceName(names map[int64]string, id *int64) string {
	if id == nil {
		return ""
	}

	return names[*id]
}
