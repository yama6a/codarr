package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// Path validation is input validation, not authorisation. plan.md 21 is
// explicit that there is no auth layer and that none may be added; what these
// checks stop is a malformed request naming a file outside the library.

// cleanPath normalises an absolute path and refuses anything that is not one.
// A relative path or a traversal segment is rejected before it is cleaned, so
// "/media/../etc/passwd" cannot arrive as an innocent-looking "/etc/passwd".
func cleanPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", badRequest("path is required")
	}

	if !strings.HasPrefix(p, "/") {
		return "", badRequest("path %q must be absolute", raw)
	}

	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", &Error{
				Status:  http.StatusBadRequest,
				Code:    "path_traversal",
				Message: "path " + raw + " contains a .. segment",
			}
		}
	}

	cleaned := pathmap.Normalise(p)
	if cleaned == "" {
		return "", badRequest("path %q is not a usable absolute path", raw)
	}

	return cleaned, nil
}

// underRoots rejects a path that lies under no configured root. Every path a
// request supplies is checked, because the roots are the whole of the
// filesystem this process has any business touching.
func (s *Server) underRoots(ctx context.Context, raw string) (string, error) {
	cleaned, err := cleanPath(raw)
	if err != nil {
		return "", err
	}

	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		return "", fmt.Errorf("list roots: %w", err)
	}

	if _, ok := pathmap.Attribute(roots, cleaned); !ok {
		return "", &Error{
			Status:  http.StatusBadRequest,
			Code:    "path_outside_roots",
			Message: "path " + cleaned + " is not under any configured root",
		}
	}

	return cleaned, nil
}

// rootPath validates a path being registered as a root. It cannot be checked
// against the roots, since it is about to become one, so only the shape is
// enforced, plus a refusal to nest inside a root that already exists: two roots
// covering the same file make attribution ambiguous (plan.md 16.2).
func (s *Server) rootPath(ctx context.Context, raw string) (string, error) {
	cleaned, err := cleanPath(raw)
	if err != nil {
		return "", err
	}

	roots, err := s.store.ListRoots(ctx)
	if err != nil {
		return "", fmt.Errorf("list roots: %w", err)
	}

	for _, r := range roots {
		existing := pathmap.Normalise(r.Path)
		if existing == cleaned {
			return "", conflict("root_exists", "root %s already exists", cleaned)
		}

		if contains(existing, cleaned) || contains(cleaned, existing) {
			return "", conflict("root_overlaps", "root %s overlaps the existing root %s", cleaned, existing)
		}
	}

	return cleaned, nil
}

func contains(parent, child string) bool {
	return parent != "" && strings.HasPrefix(child, parent+"/")
}

// mappingPath validates one side of a path mapping. The remote side belongs to
// another service's filesystem, so it is not checked against the roots; it only
// has to be an absolute path.
func mappingPath(raw, side string) (string, error) {
	cleaned, err := cleanPath(raw)
	if err != nil {
		return "", badRequest("%s path mapping: %s", side, err.Error())
	}

	return cleaned, nil
}

// settingsDir validates a configured directory such as the temp dir. It is not
// under a root by design: plan.md 15.1 puts it on a different filesystem.
func settingsDir(raw, name string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", badRequest("%s is required", name)
	}

	cleaned, err := cleanPath(raw)
	if err != nil {
		return "", badRequest("%s: %s", name, err.Error())
	}

	return filepath.Clean(cleaned), nil
}
