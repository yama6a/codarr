package plex_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
	"github.com/yama6a/codarr/internal/plex"
)

const testToken = "PLACEHOLDER-PLEX-TOKEN"

// route is one canned answer, keyed by method and path.
type route struct {
	status  int
	fixture string
	body    string
}

// recorded is one request the server saw, minus anything secret.
type recorded struct {
	Method string
	Path   string
	Query  string
	Token  string
	Accept string
}

type server struct {
	*httptest.Server

	mu       sync.Mutex
	routes   map[string]route
	requests []recorded
	handler  func(w http.ResponseWriter, r *http.Request) bool
}

func newServer(t *testing.T, routes map[string]route) *server {
	t.Helper()

	s := &server{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)

	return s
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, recorded{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Token:  r.Header.Get("X-Plex-Token"),
		Accept: r.Header.Get("Accept"),
	})
	handler := s.handler
	rt, ok := s.routes[r.Method+" "+r.URL.Path]
	s.mu.Unlock()

	if handler != nil && handler(w, r) {
		return
	}

	if !ok {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	if rt.status == 0 {
		rt.status = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rt.status)

	switch {
	case rt.fixture != "":
		_, _ = w.Write(readFixture(rt.fixture))
	case rt.body != "":
		_, _ = w.Write([]byte(rt.body))
	}
}

func (s *server) seen() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]recorded(nil), s.requests...)
}

func (s *server) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.requests)
}

func readFixture(name string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}

	return b
}

// sectionRoutes is the live section list, which every path-resolving test needs.
func sectionRoutes() map[string]route {
	return map[string]route{
		"GET /library/sections": {fixture: "sections.json"},
	}
}

func merge(dst map[string]route, src map[string]route) map[string]route {
	for k, v := range src {
		dst[k] = v
	}

	return dst
}

func newClient(t *testing.T, s *server, mutate func(*plex.Config)) *plex.Client {
	t.Helper()

	cfg := plex.Config{
		BaseURL:      s.URL,
		Token:        testToken,
		Mapper:       pathmap.New(nil),
		Clock:        clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
		RefreshAfter: true,
		AnalyzeAfter: true,
		Retry:        plex.Retry{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond},
	}

	if mutate != nil {
		mutate(&cfg)
	}

	c, err := plex.New(cfg)
	require.NoError(t, err)

	return c
}

// mappedMapper is the mapping this cluster does not need. VERIFY.md records
// Plex seeing the same paths Codarr does, so the reverse step is a no-op here;
// the tests still exercise it, because that is one mount change from mattering.
func mappedMapper() *pathmap.Mapper {
	return pathmap.New([]domain.PathMapping{
		{Local: "/mnt/pool/media", Remote: "/media"},
	})
}
