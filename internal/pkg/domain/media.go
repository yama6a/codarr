package domain

import "time"

// MediaStatus is the lifecycle of a file as Codarr sees it.
type MediaStatus string

const (
	MediaNew        MediaStatus = "new"
	MediaAnalyzed   MediaStatus = "analyzed"
	MediaQueued     MediaStatus = "queued"
	MediaProcessing MediaStatus = "processing"
	MediaDone       MediaStatus = "done"
	MediaFailed     MediaStatus = "failed"
	MediaIgnored    MediaStatus = "ignored"
	MediaSkipped    MediaStatus = "skipped"
	MediaMissing    MediaStatus = "missing"
)

// Provenance answers "is this file still byte-identical to what Codarr wrote?".
// It is derived on every analysis, never set by a user.
type Provenance string

const (
	// ProvenanceUntouched means codarr_output_fingerprint IS NULL: Codarr has
	// never written this file.
	ProvenanceUntouched Provenance = "untouched"
	// ProvenanceCodarrOutput means the current fingerprint still matches what
	// was recorded at promotion.
	ProvenanceCodarrOutput Provenance = "codarr_output"
	// ProvenanceModified means something rewrote the file after Codarr produced
	// it, a Bazarr subtitle embed being the realistic case.
	ProvenanceModified Provenance = "modified_since_transcode"
)

// DeriveProvenance is the single definition of the rule, so the store, the
// analyzer and the UI cannot drift apart.
func DeriveProvenance(recordedOutputFingerprint, currentFingerprint string) Provenance {
	switch recordedOutputFingerprint {
	case "":
		return ProvenanceUntouched
	case currentFingerprint:
		return ProvenanceCodarrOutput
	default:
		return ProvenanceModified
	}
}

// BitrateSource records which rung of the resolution chain produced a video
// bitrate, because ffprobe's per-stream bit_rate is usually absent for Matroska.
type BitrateSource string

const (
	BitrateFromStream   BitrateSource = "stream"
	BitrateFromBPSTag   BitrateSource = "bps_tag"
	BitrateFromComputed BitrateSource = "computed"
	BitrateFromFormat   BitrateSource = "format"
	BitrateUnresolved   BitrateSource = "unresolved"
)

// MediaFile is one file on disk, keyed on its absolute local path.
type MediaFile struct {
	ID            int64
	Path          string
	RootID        *int64
	ArrInstanceID *int64
	ArrEntityID   *int64

	SizeBytes int64
	MTime     int64
	NLink     int

	Fingerprint     string
	FingerprintAlgo string

	ProbeJSON     string
	MediaInfoJSON string
	AnalyzedAt    *time.Time

	Plan        *Plan
	PlanKind    Kind
	PlanReasons []string

	Container       string
	VideoCodec      string
	VideoProfile    string
	VideoLevel      string
	VideoBitrate    int
	VideoBitrateSrc BitrateSource
	IsHDR           bool

	CodarrTagged     bool
	CodarrPolicyHash string

	CodarrJobID             *int64
	CodarrProcessedAt       *time.Time
	CodarrOutputFingerprint string
	CodarrOutputSize        int64
	CodarrOutputMTime       int64
	CodarrOutputFullHash    string

	Provenance         Provenance
	IntegrityCheckedAt *time.Time

	Status    MediaStatus
	Ignored   bool
	LastError string

	CreatedAt time.Time
	UpdatedAt time.Time
}
