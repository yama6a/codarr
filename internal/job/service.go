package job

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/pkg/clock"
)

// DefaultIdlePoll is the backstop wait when the queue is empty or paused; every
// control that creates work also wakes the worker.
const DefaultIdlePoll = 2 * time.Second

// Deps is everything the queue needs. Nothing is instantiated internally; see
// CLAUDE.md, everything is wired in cmd/codarr/main.go.
type Deps struct {
	Store         Store
	Prober        Prober
	Promoter      Promoter
	FS            FS
	Fingerprinter Fingerprinter
	Notifier      Notifier
	Hardware      Hardware
	Analyzer      Analyzer
	NewEncoder    NewEncoder
	Clock         clock.Clock
	Logger        *slog.Logger

	// Metrics is optional. A nil value records nothing and is safe everywhere.
	Metrics Metrics

	// Version is stamped into every output as CODARR_VERSION (12).
	Version string

	IdlePoll time.Duration
}

// Service is the queue: the single worker goroutine of plan.md 19, running exactly
// one transcode at a time, plus the operations the API drives it with.
type Service struct {
	store    Store
	prober   Prober
	promoter Promoter
	fs       FS
	fp       Fingerprinter
	notifier Notifier
	hw       Hardware
	analyzer Analyzer
	newEnc   NewEncoder
	clk      clock.Clock
	log      *slog.Logger
	mx       recorder
	version  string
	idlePoll time.Duration

	engine decide.Engine
	est    estimator

	mu      sync.Mutex
	current *running
	pending []int64

	wake chan struct{}
}

// running is the job the worker has in flight, and the two things a cancel
// needs to reach it.
type running struct {
	id          int64
	stagingPath string
	cancel      context.CancelFunc
	requested   bool
	done        chan struct{}
}

// New returns the queue.
func New(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}

	if d.IdlePoll <= 0 {
		d.IdlePoll = DefaultIdlePoll
	}

	log := d.Logger.With(slog.String("component", "job"))

	return &Service{
		store:    d.Store,
		prober:   d.Prober,
		promoter: d.Promoter,
		fs:       d.FS,
		fp:       d.Fingerprinter,
		notifier: d.Notifier,
		hw:       d.Hardware,
		analyzer: d.Analyzer,
		newEnc:   d.NewEncoder,
		clk:      d.Clock,
		log:      log,
		mx:       recorder{m: d.Metrics},
		version:  d.Version,
		idlePoll: d.IdlePoll,
		engine:   decide.New(),
		est:      estimator{store: d.Store, clk: d.Clock, log: log},
		wake:     make(chan struct{}, 1),
	}
}

// notify wakes the worker without blocking; the channel is a signal, not a queue,
// since one pending wake-up is all a poll loop can use.
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) markRunning(id int64, cancel context.CancelFunc) *running {
	r := &running{id: id, cancel: cancel, done: make(chan struct{})}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.current = r

	return r
}

func (s *Service) setStaging(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current != nil {
		s.current.stagingPath = path
	}
}

func (s *Service) clearRunning(r *running) {
	s.mu.Lock()
	s.current = nil
	s.mu.Unlock()

	close(r.done)
}

// cancelRequested reports that this job stopped because someone asked it to,
// which is what separates cancelled from an interrupted shutdown.
func (s *Service) cancelRequested(r *running) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return r.requested
}

func (s *Service) addPending(ids ...int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pending = append(s.pending, ids...)
}

func (s *Service) nextPending() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) == 0 {
		return 0, false
	}

	id := s.pending[0]
	s.pending = s.pending[1:]

	return id, true
}
