package arr

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
	"github.com/yama6a/codarr/internal/pkg/pathmap"
)

// EventType is the *arr's eventType field, kept as a string rather than a
// closed enum. New event types arrive with new *arr releases and an unknown one
// has to be ignored, not rejected.
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

// Event is one webhook, reduced to what Codarr reads. The *arr payloads carry
// far more; declaring only these fields is deliberate, because they are
// external schemas that vary by event type and by *arr version and mirroring
// them exactly would break on every release (plan.md 13.1).
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

// EventFile is one file the event is about. RemotePath is the sending
// instance's view of the filesystem and has to be rewritten with THAT
// instance's mappings before it means anything to Codarr.
type EventFile struct {
	ID                 int64
	RelativePath       string
	RemotePath         string
	PreviousRemotePath string
}

// LocalFile is an EventFile after this instance's mappings. Mapped is false
// when no mapping matched, which for a real path is a configuration error and
// for the Test payload's "C:\testpath" is simply expected.
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

// ParseWebhook turns one raw webhook body into a typed event. It takes the
// flavour rather than sniffing the payload because the receiving URL already
// identifies the instance exactly (plan.md 13.1).
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

		// Rename carries renamedMovieFiles and no movieFile at all, which
		// plan.md 13.1 does not mention. Without this a rename is parsed as an
		// event with no files and the stored path silently goes stale.
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

// event builds the file, falling back to folder plus relativePath when the
// payload omits path. Both *arrs set it today; the fallback costs one branch
// and removes a dependency on that staying true.
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

// validate only rejects events Codarr would act on with nothing to act upon. A
// Test carries a placeholder Windows path and no file, and an unhandled type is
// allowed to be empty.
func validate(e Event) error {
	if !e.Handled() || e.Type == EventTest {
		return nil
	}

	if len(e.Files) == 0 {
		return fmt.Errorf("%w: %s %s carried no file path", ErrBadPayload, e.Flavour, e.Type)
	}

	return nil
}
