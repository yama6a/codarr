package arr

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// RootFolder is one entry from GET /api/v3/rootfolder, already run through the
// instance's path mappings.
type RootFolder struct {
	ID         int64
	Accessible bool
	FreeSpace  *int64
	Imported   pathmap.ImportedRoot
}

//nolint:tagliatelle // Radarr and Sonarr speak camelCase; the repo's snake_case rule cannot apply to a foreign schema.
type rootFolderResource struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  *int64 `json:"freeSpace"`
}

// RootFolders lists the instance's root folders, mapped here because the reported path
// alone is unusable: all four instances report "/media" (VERIFY.md, plan.md 16.2).
func (a *API) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var resources []rootFolderResource

	err := a.tr.do(ctx, call{method: http.MethodGet, path: "/api/v3/rootfolder", out: &resources})
	if err != nil {
		return nil, fmt.Errorf("arr: listing root folders on %s failed: %w", a.identity.Name, err)
	}

	reported := make([]string, 0, len(resources))
	for _, r := range resources {
		reported = append(reported, r.Path)
	}

	mapped := pathmap.ImportRoots(a.mapper, a.identity.ID, reported)

	out := make([]RootFolder, 0, len(mapped))

	for i, m := range mapped {
		out = append(out, RootFolder{
			ID:         resources[i].ID,
			Accessible: resources[i].Accessible,
			FreeSpace:  resources[i].FreeSpace,
			Imported:   m,
		})
	}

	return out, nil
}

// ImportRoots is the import action of plan.md 16.2, refusing the whole import when any
// path came back unmapped, which is the state where attribution stops working.
func ImportRoots(ctx context.Context, c Client) ([]pathmap.ImportedRoot, error) {
	folders, err := c.RootFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("arr: importing root folders failed: %w", err)
	}

	id := c.Identity()

	var (
		out      = make([]pathmap.ImportedRoot, 0, len(folders))
		unmapped []string
	)

	for _, f := range folders {
		if !f.Imported.Mapped {
			unmapped = append(unmapped, f.Imported.ReportedPath)
		}

		out = append(out, f.Imported)
	}

	if len(unmapped) > 0 {
		return out, fmt.Errorf(
			"%w: %s reports %s, which no mapping rewrites. Add a path mapping for this instance before importing, "+
				"or every instance will claim the same root", ErrNoPathMapping, id.Name, strings.Join(unmapped, ", "))
	}

	return out, nil
}
