package arr

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// EventType is a string rather than a closed enum, because new event types arrive with
// new *arr releases and an unknown one has to be ignored, not rejected.
type EventType string

// The events Codarr acts on (plan.md 13.1). Everything else parses fine and
// reports Handled false.
const (
	EventTest              EventType = "Test"
	EventDownload          EventType = "Download"
	EventRename            EventType = "Rename"
	EventMovieFileDelete   EventType = "MovieFileDelete"
	EventEpisodeFileDelete EventType = "EpisodeFileDelete"
)

// Event is one webhook reduced to what Codarr reads; the payloads carry far more, and
// mirroring an external schema would break on every *arr release (plan.md 13.1).
type Event struct {
	Flavour      domain.Flavour
	Type         EventType
	InstanceName string
	Title        string
	IsUpgrade    bool
	DeleteReason string
	Item         ItemRef
	Files        []EventFile
}

// EventFile is one file the event is about; RemotePath is the sending instance's view
// and means nothing until rewritten with that same instance's mappings.
type EventFile struct {
	ID                 int64
	RelativePath       string
	RemotePath         string
	PreviousRemotePath string
}

// LocalFile is an EventFile after this instance's mappings; Mapped false is a config
// error for a real path and expected for the Test payload's "C:\testpath".
type LocalFile struct {
	ID           int64
	Path         string
	PreviousPath string
	Mapped       bool
}

// Handled reports whether this is one of the five event types Codarr acts on.
func (e Event) Handled() bool {
	switch e.Type {
	case EventTest, EventDownload, EventRename, EventMovieFileDelete, EventEpisodeFileDelete:
		return true
	default:
		return false
	}
}

// Deletion reports whether the event means a file is gone, which marks the
// media_files row missing rather than removing it (plan.md 13.2).
func (e Event) Deletion() bool {
	return e.Type == EventMovieFileDelete || e.Type == EventEpisodeFileDelete
}

// Local rewrites every file into Codarr's view with the mappings of the
// instance that sent the event (plan.md 13.1).
func (e Event) Local(m *pathmap.Mapper) []LocalFile {
	if m == nil {
		m = pathmap.New(nil)
	}

	out := make([]LocalFile, 0, len(e.Files))

	for _, f := range e.Files {
		local, mapped := m.ToLocal(f.RemotePath)

		lf := LocalFile{ID: f.ID, Path: local, Mapped: mapped}
		if f.PreviousRemotePath != "" {
			lf.PreviousPath, _ = m.ToLocal(f.PreviousRemotePath)
		}

		out = append(out, lf)
	}

	return out
}

// ParseWebhook takes the flavour rather than sniffing the payload, because the
// receiving URL already identifies the instance exactly (plan.md 13.1).
func ParseWebhook(flavour domain.Flavour, body []byte) (Event, error) {
	switch flavour {
	case domain.FlavourRadarr:
		return parseRadarr(body)
	case domain.FlavourSonarr:
		return parseSonarr(body)
	default:
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownFlavour, flavour)
	}
}

//nolint:tagliatelle // Radarr and Sonarr speak camelCase; the repo's snake_case rule cannot apply to a foreign schema.
type (
	radarrPayload struct {
		EventType    string `json:"eventType"`
		InstanceName string `json:"instanceName"`
		IsUpgrade    bool   `json:"isUpgrade"`
		DeleteReason string `json:"deleteReason"`
		Movie        *struct {
			ID         int64  `json:"id"`
			Title      string `json:"title"`
			FolderPath string `json:"folderPath"`
		} `json:"movie"`
		MovieFile *arrFile `json:"movieFile"`

		// Rename carries renamedMovieFiles and no movieFile, which plan.md 13.1 omits.
		// Without this a rename parses as an event with no files and the path goes stale.
		RenamedMovieFiles []arrFile `json:"renamedMovieFiles"`
	}

	sonarrPayload struct {
		EventType    string `json:"eventType"`
		InstanceName string `json:"instanceName"`
		IsUpgrade    bool   `json:"isUpgrade"`
		DeleteReason string `json:"deleteReason"`
		Series       *struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Path  string `json:"path"`
		} `json:"series"`
		Episodes []struct {
			ID int64 `json:"id"`
		} `json:"episodes"`
		EpisodeFile *arrFile `json:"episodeFile"`

		// See the note on RenamedMovieFiles.
		RenamedEpisodeFiles []arrFile `json:"renamedEpisodeFiles"`
	}

	arrFile struct {
		ID           int64  `json:"id"`
		RelativePath string `json:"relativePath"`
		Path         string `json:"path"`
		PreviousPath string `json:"previousPath"`
	}
)

func parseRadarr(body []byte) (Event, error) {
	var p radarrPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("%w: radarr: %w", ErrBadPayload, err)
	}

	e := Event{
		Flavour:      domain.FlavourRadarr,
		Type:         EventType(p.EventType),
		InstanceName: p.InstanceName,
		IsUpgrade:    p.IsUpgrade,
		DeleteReason: p.DeleteReason,
	}

	var folder string
	if p.Movie != nil {
		e.Title = p.Movie.Title
		e.Item.MovieID = p.Movie.ID
		folder = p.Movie.FolderPath
	}

	e.Files = collectFiles(folder, p.MovieFile, p.RenamedMovieFiles)

	return e, validate(e)
}

func parseSonarr(body []byte) (Event, error) {
	var p sonarrPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("%w: sonarr: %w", ErrBadPayload, err)
	}

	e := Event{
		Flavour:      domain.FlavourSonarr,
		Type:         EventType(p.EventType),
		InstanceName: p.InstanceName,
		IsUpgrade:    p.IsUpgrade,
		DeleteReason: p.DeleteReason,
	}

	var folder string
	if p.Series != nil {
		e.Title = p.Series.Title
		e.Item.SeriesID = p.Series.ID
		folder = p.Series.Path
	}

	for _, ep := range p.Episodes {
		if ep.ID != 0 {
			e.Item.EpisodeIDs = append(e.Item.EpisodeIDs, ep.ID)
		}
	}

	e.Files = collectFiles(folder, p.EpisodeFile, p.RenamedEpisodeFiles)

	return e, validate(e)
}

func collectFiles(folder string, single *arrFile, renamed []arrFile) []EventFile {
	files := make([]EventFile, 0, len(renamed)+1)

	if single != nil {
		if f, ok := single.event(folder); ok {
			files = append(files, f)
		}
	}

	for _, r := range renamed {
		if f, ok := r.event(folder); ok {
			files = append(files, f)
		}
	}

	return files
}

// Falls back to folder plus relativePath when the payload omits path: both *arrs set it
// today, and one branch removes the dependency on that staying true.
func (f arrFile) event(folder string) (EventFile, bool) {
	full := strings.TrimSpace(f.Path)
	if full == "" && folder != "" && f.RelativePath != "" {
		full = path.Join(folder, f.RelativePath)
	}

	if full == "" {
		return EventFile{}, false
	}

	return EventFile{
		ID:                 f.ID,
		RelativePath:       f.RelativePath,
		RemotePath:         full,
		PreviousRemotePath: strings.TrimSpace(f.PreviousPath),
	}, true
}

// Only events Codarr acts on are rejected for being empty: a Test carries a placeholder
// path and no file, and an unhandled type is allowed to be empty.
func validate(e Event) error {
	if !e.Handled() || e.Type == EventTest {
		return nil
	}

	if len(e.Files) == 0 {
		return fmt.Errorf("%w: %s %s carried no file path", ErrBadPayload, e.Flavour, e.Type)
	}

	return nil
}
