package arr_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yama6a/codarr/internal/arr"
	"github.com/yama6a/codarr/internal/pkg/clock"
	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

const testAPIKey = "PLACEHOLDER0API0KEY0000000000000"

type route struct {
	status  int
	fixture string
	body    string
}

type recorded struct {
	Method string
	Path   string
	APIKey string
	Accept string
	Body   string
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
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	s.requests = append(s.requests, recorded{
		Method: r.Method,
		Path:   r.URL.Path,
		APIKey: r.Header.Get("X-Api-Key"),
		Accept: r.Header.Get("Accept"),
		Body:   string(body),
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
		_, _ = w.Write(fixture(rt.fixture))
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

func fixture(name string) []byte {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}

	return b
}

// radarrYama mirrors the live instance: it reports the literal "/media" and
// only its own mapping says which slice of the library that really is.
func radarrYama() domain.ArrInstance {
	return domain.ArrInstance{
		ID:      1,
		Name:    "radarr-yama",
		Flavour: domain.FlavourRadarr,
		APIKey:  testAPIKey,
		Enabled: true,
	}
}

func sonarrYama() domain.ArrInstance {
	return domain.ArrInstance{
		ID:      3,
		Name:    "sonarr-yama",
		Flavour: domain.FlavourSonarr,
		APIKey:  testAPIKey,
		Enabled: true,
	}
}

func radarrMapper() *pathmap.Mapper {
	return pathmap.New([]domain.PathMapping{{Local: "/media/yama/movies", Remote: "/media"}})
}

func sonarrMapper() *pathmap.Mapper {
	return pathmap.New([]domain.PathMapping{{Local: "/media/yama/tv", Remote: "/media"}})
}

func newClient(t *testing.T, s *server, instance domain.ArrInstance, mapper *pathmap.Mapper) *arr.API {
	t.Helper()

	instance.BaseURL = s.URL

	c, err := arr.New(arr.Config{
		Instance: instance,
		Mapper:   mapper,
		Clock:    clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)),
		Retry:    arr.Retry{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond},
	})
	require.NoError(t, err)

	return c
}
