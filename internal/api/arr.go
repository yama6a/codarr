package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// webhookIDBytes is how much entropy the per-instance webhook id carries. It is
// the only thing standing between an unauthenticated POST and Codarr ingesting
// somebody else's payload, so it is generated server-side and never chosen.
const webhookIDBytes = 16

// ListArrInstances returns every configured instance, API keys masked (18.4).
func (s *Server) ListArrInstances(
	ctx context.Context, _ gen.ListArrInstancesRequestObject,
) (gen.ListArrInstancesResponseObject, error) {
	instances, err := s.store.ListArrInstances(ctx)
	if err != nil {
		return gen.ListArrInstancesdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out := make([]gen.ArrInstance, 0, len(instances))

	for _, in := range instances {
		mappings, err := s.store.ListArrPathMappings(ctx, in.ID)
		if err != nil {
			return gen.ListArrInstancesdefaultJSONResponse(s.fail(ctx, err)), nil
		}

		out = append(out, arrInstance(in, mappings))
	}

	return gen.ListArrInstances200JSONResponse(out), nil
}

// GetArrInstance returns one instance.
func (s *Server) GetArrInstance(
	ctx context.Context, req gen.GetArrInstanceRequestObject,
) (gen.GetArrInstanceResponseObject, error) {
	out, err := s.arrView(ctx, req.Id)
	if err != nil {
		return gen.GetArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetArrInstance200JSONResponse(out), nil
}

// CreateArrInstance adds an instance. The webhook id is generated here rather
// than accepted from the request, so it cannot be guessed or chosen.
func (s *Server) CreateArrInstance(
	ctx context.Context, req gen.CreateArrInstanceRequestObject,
) (gen.CreateArrInstanceResponseObject, error) {
	if req.Body == nil {
		return gen.CreateArrInstancedefaultJSONResponse(s.fail(ctx, badRequest("an instance body is required"))), nil
	}

	instance, mappings, err := newArrInstance(*req.Body)
	if err != nil {
		return gen.CreateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	created, err := s.store.CreateArrInstance(ctx, instance)
	if err != nil {
		return gen.CreateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if err := s.store.ReplaceArrPathMappings(ctx, created.ID, mappings); err != nil {
		return gen.CreateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out, err := s.arrView(ctx, created.ID)
	if err != nil {
		return gen.CreateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.CreateArrInstance201JSONResponse(out), nil
}

// UpdateArrInstance replaces an instance and its whole mapping list. The API key
// is only overwritten when the submitted value is not the mask (18.4).
func (s *Server) UpdateArrInstance(
	ctx context.Context, req gen.UpdateArrInstanceRequestObject,
) (gen.UpdateArrInstanceResponseObject, error) {
	if req.Body == nil {
		return gen.UpdateArrInstancedefaultJSONResponse(s.fail(ctx, badRequest("an instance body is required"))), nil
	}

	current, err := s.store.GetArrInstance(ctx, req.Id)
	if err != nil {
		return gen.UpdateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	updated, mappings, err := applyArrInstance(current, *req.Body)
	if err != nil {
		return gen.UpdateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if err := s.store.UpdateArrInstance(ctx, updated); err != nil {
		return gen.UpdateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if err := s.store.ReplaceArrPathMappings(ctx, req.Id, mappings); err != nil {
		return gen.UpdateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out, err := s.arrView(ctx, req.Id)
	if err != nil {
		return gen.UpdateArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.UpdateArrInstance200JSONResponse(out), nil
}

// DeleteArrInstance removes an instance. Its roots keep their rows; the files
// under them are still processed, nothing is notified afterwards.
func (s *Server) DeleteArrInstance(
	ctx context.Context, req gen.DeleteArrInstanceRequestObject,
) (gen.DeleteArrInstanceResponseObject, error) {
	if err := s.store.DeleteArrInstance(ctx, req.Id); err != nil {
		return gen.DeleteArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.DeleteArrInstance204Response{}, nil
}

// TestArrInstance reads the instance's system status, which proves both that it
// answers and that the API key is accepted.
func (s *Server) TestArrInstance(
	ctx context.Context, req gen.TestArrInstanceRequestObject,
) (gen.TestArrInstanceResponseObject, error) {
	client, err := s.arrClient(ctx, req.Id)
	if err != nil {
		return gen.TestArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	res := client.Test(ctx)
	now := s.clk.Now()

	if err := s.store.SetArrTestResult(ctx, req.Id, now, res.Message); err != nil {
		return gen.TestArrInstancedefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.TestArrInstance200JSONResponse{
		Message:       res.Message,
		Ok:            res.OK,
		ServerName:    strPtr(res.AppName),
		ServerVersion: strPtr(res.Version),
		TestedAt:      now,
	}, nil
}

// ListArrRootFolders is a live query, already mapped into Codarr's view. Every
// live instance reports the literal "/media" (VERIFY.md), so the mapping is what
// makes the answer mean anything.
func (s *Server) ListArrRootFolders(
	ctx context.Context, req gen.ListArrRootFoldersRequestObject,
) (gen.ListArrRootFoldersResponseObject, error) {
	folders, existing, err := s.arrRootFolders(ctx, req.Id)
	if err != nil {
		return gen.ListArrRootFoldersdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	out := make([]gen.ArrRootFolder, 0, len(folders))
	for _, f := range folders {
		out = append(out, gen.ArrRootFolder{
			Accessible:      f.Accessible,
			AlreadyImported: ptrOf(existing[pathmap.Normalise(f.Imported.Path)]),
			FreeSpace:       f.FreeSpace,
			Id:              f.ID,
			LocalPath:       f.Imported.Path,
			Path:            f.Imported.ReportedPath,
		})
	}

	return gen.ListArrRootFolders200JSONResponse(out), nil
}

// ImportArrRoots creates roots from the instance's root folders. A path another
// enabled instance already claims is skipped and surfaced rather than guessed
// at (plan.md 16.2).
func (s *Server) ImportArrRoots(
	ctx context.Context, req gen.ImportArrRootsRequestObject,
) (gen.ImportArrRootsResponseObject, error) {
	folders, _, err := s.arrRootFolders(ctx, req.Id)
	if err != nil {
		return gen.ImportArrRootsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		return gen.ImportArrRootsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	instances, err := s.instanceNames(ctx)
	if err != nil {
		return gen.ImportArrRootsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	result := gen.ImportRootsResult{Conflicts: []gen.RootConflict{}, Roots: []gen.Root{}}

	for _, f := range folders {
		if !f.Imported.Mapped {
			return gen.ImportArrRootsdefaultJSONResponse(s.fail(ctx, fmt.Errorf(
				"%w: %s reports %s, which no mapping rewrites",
				arr.ErrNoPathMapping, instances[req.Id], f.Imported.ReportedPath))), nil
		}

		created, err := s.importRoot(ctx, f, req.Id, roots, instances, &result)
		if err != nil {
			return gen.ImportArrRootsdefaultJSONResponse(s.fail(ctx, err)), nil
		}

		if created != nil {
			roots = append(roots, *created)
		}
	}

	return gen.ImportArrRoots200JSONResponse(result), nil
}

// importRoot decides what to do with one reported folder and returns the root it
// created, if any, so the caller can keep the ownership view current within the
// loop.
func (s *Server) importRoot(
	ctx context.Context, f arr.RootFolder, instanceID int64,
	roots []domain.Root, instances map[int64]string, result *gen.ImportRootsResult,
) (*domain.Root, error) {
	owner, taken := ownerOf(roots, f.Imported.Path)

	if taken {
		if owner == instanceID {
			result.Skipped++

			return nil, nil //nolint:nilnil // already ours, nothing created
		}

		result.Conflicts = append(result.Conflicts, gen.RootConflict{
			OwningArrInstanceId:   owner,
			OwningArrInstanceName: instances[owner],
			Path:                  f.Imported.Path,
		})

		return nil, nil //nolint:nilnil // surfaced rather than guessed at (plan.md 16.2)
	}

	created, err := s.store.CreateRoot(ctx, f.Imported.Root())
	if err != nil {
		return nil, fmt.Errorf("create root %s: %w", f.Imported.Path, err)
	}

	result.Imported++
	result.Roots = append(result.Roots, root(created, instances[instanceID], nil))

	return &created, nil
}

// ownerOf reports which instance already claims a path, and whether any does.
func ownerOf(roots []domain.Root, path string) (int64, bool) {
	norm := pathmap.Normalise(path)

	for _, r := range roots {
		if pathmap.Normalise(r.Path) != norm {
			continue
		}

		if r.ArrInstanceID == nil {
			return 0, true
		}

		return *r.ArrInstanceID, true
	}

	return 0, false
}

func (s *Server) arrRootFolders(ctx context.Context, id int64) ([]arr.RootFolder, map[string]bool, error) {
	client, err := s.arrClient(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	folders, err := client.RootFolders(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list root folders: %w", err)
	}

	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list roots: %w", err)
	}

	existing := make(map[string]bool, len(roots))
	for _, r := range roots {
		existing[pathmap.Normalise(r.Path)] = true
	}

	return folders, existing, nil
}

func (s *Server) arrView(ctx context.Context, id int64) (gen.ArrInstance, error) {
	instance, err := s.store.GetArrInstance(ctx, id)
	if err != nil {
		return gen.ArrInstance{}, fmt.Errorf("get arr instance %d: %w", id, err)
	}

	mappings, err := s.store.ListArrPathMappings(ctx, id)
	if err != nil {
		return gen.ArrInstance{}, fmt.Errorf("list path mappings for arr instance %d: %w", id, err)
	}

	return arrInstance(instance, mappings), nil
}

func (s *Server) arrClient(ctx context.Context, id int64) (ArrClient, error) {
	if s.arr == nil {
		return nil, fmt.Errorf("%w: no *arr client factory wired", arr.ErrNotConfigured)
	}

	instance, err := s.store.GetArrInstance(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get arr instance %d: %w", id, err)
	}

	client, err := s.arr(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("build client for arr instance %d: %w", id, err)
	}

	return client, nil
}

func (s *Server) instanceNames(ctx context.Context) (map[int64]string, error) {
	instances, err := s.store.ListArrInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("list arr instances: %w", err)
	}

	out := make(map[int64]string, len(instances))
	for _, in := range instances {
		out[in.ID] = in.Name
	}

	return out, nil
}

func newArrInstance(in gen.ArrInstanceCreate) (domain.ArrInstance, []domain.PathMapping, error) {
	flavour, err := flavourOf(in.Flavour)
	if err != nil {
		return domain.ArrInstance{}, nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.ArrInstance{}, nil, badRequest("name is required")
	}

	if strings.TrimSpace(in.ApiKey) == "" || in.ApiKey == domain.MaskedSecret {
		return domain.ArrInstance{}, nil, badRequest("api_key is required when creating an instance")
	}

	var mappings []domain.PathMapping

	if in.PathMappings != nil {
		mappings, err = domainMappings(*in.PathMappings)
		if err != nil {
			return domain.ArrInstance{}, nil, err
		}
	}

	webhookID, err := newWebhookID()
	if err != nil {
		return domain.ArrInstance{}, nil, err
	}

	return domain.ArrInstance{
		Name:           name,
		Flavour:        flavour,
		BaseURL:        strings.TrimSpace(in.BaseUrl),
		APIKey:         strings.TrimSpace(in.ApiKey),
		WebhookID:      webhookID,
		RescanAfter:    boolOr(in.RescanAfter, true),
		UnmonitorAfter: boolOr(in.UnmonitorAfter, false),
		Enabled:        boolOr(in.Enabled, true),
	}, mappings, nil
}

func applyArrInstance(
	current domain.ArrInstance, in gen.ArrInstanceUpdate,
) (domain.ArrInstance, []domain.PathMapping, error) {
	flavour, err := flavourOf(in.Flavour)
	if err != nil {
		return domain.ArrInstance{}, nil, err
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.ArrInstance{}, nil, badRequest("name is required")
	}

	mappings, err := domainMappings(in.PathMappings)
	if err != nil {
		return domain.ArrInstance{}, nil, err
	}

	current.Name = name
	current.Flavour = flavour
	current.BaseURL = strings.TrimSpace(in.BaseUrl)
	current.APIKey = keepOrReplace(current.APIKey, in.ApiKey)
	current.RescanAfter = in.RescanAfter
	current.UnmonitorAfter = in.UnmonitorAfter
	current.Enabled = in.Enabled

	return current, mappings, nil
}

func flavourOf(f gen.Flavour) (domain.Flavour, error) {
	switch f {
	case gen.FlavourRadarr:
		return domain.FlavourRadarr, nil
	case gen.FlavourSonarr:
		return domain.FlavourSonarr, nil
	default:
		return "", badRequest("flavour must be radarr or sonarr, got %q", string(f))
	}
}

func boolOr(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}

	return *v
}

func newWebhookID() (string, error) {
	var b [webhookIDBytes]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate webhook id: %w", err)
	}

	return hex.EncodeToString(b[:]), nil
}
