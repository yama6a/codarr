package domain

import "time"

// Flavour is which *arr an instance is.
type Flavour string

const (
	FlavourRadarr Flavour = "radarr"
	FlavourSonarr Flavour = "sonarr"
)

// Settings is the single-row configuration table. Nothing here changes what
// gets transcoded or to what; that policy is hard-coded in Go.
type Settings struct {
	TempDir             string
	QSVDevice           string
	ScanEnabled         bool
	ScanCron            string
	ScanRateLimitFPS    int
	QueuePaused         bool
	PrioritiseQuickJobs bool
	FullHashEnabled     bool
	UpdatedAt           time.Time
}

// PathMapping rewrites between Codarr's view of the filesystem and some other
// service's view of it.
type PathMapping struct {
	ID     int64
	Local  string
	Remote string
	Sort   int
}

// PlexConfig is the single Plex server.
type PlexConfig struct {
	BaseURL            string
	Token              string
	ClientIdentifier   string
	RefreshAfter       bool
	AnalyzeAfter       bool
	GuardActiveStreams bool
	LastTestedAt       *time.Time
	LastTestResult     string
	UpdatedAt          time.Time
}

// ArrInstance is one Radarr or Sonarr. There are several of each, so every
// mapping, root folder and attribution is per instance from the start.
type ArrInstance struct {
	ID             int64
	Name           string
	Flavour        Flavour
	BaseURL        string
	APIKey         string
	WebhookID      string
	RescanAfter    bool
	UnmonitorAfter bool
	Enabled        bool
	LastTestedAt   *time.Time
	LastTestResult string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Root is a directory Codarr watches. A root without an instance is processed
// but nothing is notified afterwards.
type Root struct {
	ID            int64
	Path          string
	ArrInstanceID *int64
	Imported      bool
	Enabled       bool
	CreatedAt     time.Time
}

// HWCapability is one probed encoder or decoder path, cached against the
// ffmpeg version that produced it.
type HWCapability struct {
	ID            int64
	Backend       string
	Codec         string
	Profile       string
	Direction     string
	Works         bool
	Error         string
	FfmpegVersion string
	ProbedAt      time.Time
}

// Event is one line of the UI's log view. Stdout stays the source of truth; a
// write failure here must never stop the stdout line being emitted.
type Event struct {
	ID          int64
	Level       string
	Category    string
	Message     string
	MediaFileID *int64
	JobID       *int64
	CreatedAt   time.Time
}

// ThroughputStat is a rolling average feeding duration estimates.
type ThroughputStat struct {
	ID         int64
	Kind       Kind
	Encoder    string
	Resolution string
	Samples    int
	AvgValue   float64
	UpdatedAt  time.Time
}

// MaskedSecret is what a GET returns in place of an API key or the Plex token.
// A PUT carrying this exact value leaves the stored secret untouched.
const MaskedSecret = "********"
