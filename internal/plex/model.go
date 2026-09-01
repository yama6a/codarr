package plex

// The wire types below are deliberately partial: Plex answers with far more
// than this and the shape shifts between server versions, so only the fields
// Codarr actually reads are declared.
//
//nolint:tagliatelle // Plex speaks camelCase for attributes and PascalCase for child elements; the repo's snake_case rule cannot apply to a foreign schema.
type (
	sectionsResponse struct {
		MediaContainer struct {
			Directory []sectionDirectory `json:"Directory"`
		} `json:"MediaContainer"`
	}

	sectionDirectory struct {
		Key      string `json:"key"`
		Type     string `json:"type"`
		Title    string `json:"title"`
		Location []struct {
			ID   int    `json:"id"`
			Path string `json:"path"`
		} `json:"Location"`
	}

	serverResponse struct {
		MediaContainer struct {
			FriendlyName string `json:"friendlyName"`
			Version      string `json:"version"`
		} `json:"MediaContainer"`
	}

	metadataResponse struct {
		MediaContainer struct {
			Size     int        `json:"size"`
			Metadata []metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}

	metadata struct {
		RatingKey        string  `json:"ratingKey"`
		Type             string  `json:"type"`
		Title            string  `json:"title"`
		ParentTitle      string  `json:"parentTitle"`
		GrandparentTitle string  `json:"grandparentTitle"`
		Index            int     `json:"index"`
		ParentIndex      int     `json:"parentIndex"`
		Media            []media `json:"Media"`
		User             struct {
			Title string `json:"title"`
		} `json:"User"`
		Player struct {
			Title   string `json:"title"`
			Product string `json:"product"`
			State   string `json:"state"`
		} `json:"Player"`
		TranscodeSession *struct {
			VideoDecision string `json:"videoDecision"`
			AudioDecision string `json:"audioDecision"`
		} `json:"TranscodeSession"`
	}

	media struct {
		Part []part `json:"Part"`
	}

	// part is where the file path lives, and only on a direct-play session
	// (plan.md 16.1). A transcoding session's Part carries no file attribute at
	// all, which is why the guard resolves the path through the item instead.
	part struct {
		File string `json:"file"`
	}
)

func (m metadata) files() []string {
	out := make([]string, 0, len(m.Media))

	for _, md := range m.Media {
		for _, p := range md.Part {
			if p.File != "" {
				out = append(out, p.File)
			}
		}
	}

	return out
}

func (m metadata) transcoding() bool { return m.TranscodeSession != nil }
