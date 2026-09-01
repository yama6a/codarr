package promote

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yama6a/codarr/internal/pkg/domain"
)

// StagingPrefix is the dotfile prefix of every file Codarr writes next to a
// destination. plan.md 15.1: a dotfile so the *arrs and Plex ignore it, a
// sibling so rename() never crosses a filesystem boundary.
const StagingPrefix = ".codarr-staging-"

// writeProbePrefix names the dotfile the writability check of plan.md 15.4
// creates and removes. Checking mode bits would be a guess; creating a file is
// the only answer that accounts for the export options and the effective uid.
const writeProbePrefix = ".codarr-writetest-"

// SourceState is what analysis recorded about the file being replaced.
// LegacyContainer comes from the decision engine, which already classifies
// AVI, VOB, MPEG-TS and friends for -fflags +genpts (plan.md 14.1).
type SourceState struct {
	SizeBytes       int64
	MTime           int64
	Fingerprint     string
	DurationSeconds float64
	Video           *domain.VideoState
	LegacyContainer bool
}

// PreflightRequest is plan.md 15.2 step 1, run before the encode starts.
type PreflightRequest struct {
	JobID      int64
	SourcePath string
	Source     SourceState
	OutputExt  string
}

// Staging is where the encode writes and where the rename takes its argument
// from. The two differ only when the destination lacked space and the temp
// directory turned out to be on another filesystem.
type Staging struct {
	Path        string
	FinalPath   string
	UsedTempDir bool

	// CrossDevice means Path is on a different filesystem from the destination,
	// so rename(2) would return EXDEV and a copy has to happen first (15.1).
	CrossDevice bool
}

// Preflight is plan.md 15.4. Every check here runs before a single byte is
// encoded, because after the rename in 15.2 there is nothing to fall back to.
func (p *Promoter) Preflight(req PreflightRequest) (Staging, error) {
	if !filepath.IsAbs(req.SourcePath) {
		return Staging{}, fail(domain.FailPreflight, "the source path %q is not absolute", req.SourcePath)
	}

	if err := p.checkSourceUnchanged(req); err != nil {
		return Staging{}, err
	}

	destDir := filepath.Dir(req.SourcePath)

	if err := p.checkWritable(req.JobID, destDir); err != nil {
		return Staging{}, err
	}

	staging, err := p.chooseStaging(req, destDir)
	if err != nil {
		return Staging{}, err
	}

	staging.CrossDevice, err = p.crossDevice(filepath.Dir(staging.Path), destDir)
	if err != nil {
		return Staging{}, err
	}

	// plan.md 15.4 asks that the staging and destination directories report the
	// same device number. On the primary path they ARE the same directory, so
	// the literal comparison can never fail; it is kept as the tripwire 15.6
	// asks for. NFSv4 presents every export inside one pseudo-filesystem, so a
	// dataset split leaves the client-side paths looking identical while
	// rename() silently starts returning EXDEV and stops being the atomic
	// replace the whole sequence depends on.
	if staging.CrossDevice && !staging.UsedTempDir {
		return Staging{}, fail(domain.FailPreflight,
			"the staging directory %s and the destination %s report different device numbers, so the replace would not be atomic",
			filepath.Dir(staging.Path), destDir)
	}

	return staging, nil
}

func (p *Promoter) checkSourceUnchanged(req PreflightRequest) error {
	info, err := p.fs.Stat(req.SourcePath)
	if err != nil {
		return wrap(domain.FailPreflight, err, "the source %s no longer exists or cannot be stat'd", req.SourcePath)
	}

	if info.IsDir {
		return fail(domain.FailPreflight, "the source %s is a directory", req.SourcePath)
	}

	// plan.md 15.4: one stat call, and it prevents renaming over a hard-linked
	// seeding copy.
	if info.NLink != 1 {
		return fail(domain.FailPreflight,
			"the source %s has %d hard links; replacing it would damage the other copies", req.SourcePath, info.NLink)
	}

	if info.Size != req.Source.SizeBytes {
		return fail(domain.FailPreflight,
			"the source %s changed since analysis: it is now %d bytes, analysis recorded %d",
			req.SourcePath, info.Size, req.Source.SizeBytes)
	}

	if mtime := info.MTime.Unix(); mtime != req.Source.MTime {
		return fail(domain.FailPreflight,
			"the source %s changed since analysis: its modification time is now %d, analysis recorded %d",
			req.SourcePath, mtime, req.Source.MTime)
	}

	if req.Source.Fingerprint == "" {
		return nil
	}

	current, err := p.fp.Sparse(req.SourcePath)
	if err != nil {
		return wrap(domain.FailPreflight, err, "the source %s could not be fingerprinted", req.SourcePath)
	}

	if current != req.Source.Fingerprint {
		return fail(domain.FailPreflight,
			"the source %s changed since analysis: its fingerprint is now %s, analysis recorded %s",
			req.SourcePath, current, req.Source.Fingerprint)
	}

	return nil
}

func (p *Promoter) checkWritable(jobID int64, destDir string) error {
	probe := filepath.Join(destDir, writeProbePrefix+strconv.FormatInt(jobID, 10))

	f, err := p.fs.Create(probe, 0o600)
	if err != nil {
		return wrap(domain.FailPreflight, err, "the destination directory %s is not writable", destDir)
	}

	if err := f.Close(); err != nil {
		return wrap(domain.FailPreflight, err, "the writability probe %s could not be closed", probe)
	}

	if err := p.fs.Remove(probe); err != nil {
		return wrap(domain.FailPreflight, err, "the writability probe %s could not be removed", probe)
	}

	return nil
}

func (p *Promoter) chooseStaging(req PreflightRequest, destDir string) (Staging, error) {
	name := StagingPrefix + strconv.FormatInt(req.JobID, 10) + ext(req.OutputExt)
	onDest := filepath.Join(destDir, name)
	need := uint64(float64(req.Source.SizeBytes) * SpaceFactor)

	destFree, err := p.free(destDir)
	if err != nil {
		return Staging{}, err
	}

	if destFree >= need {
		return Staging{Path: onDest, FinalPath: onDest}, nil
	}

	if p.tempDir == "" {
		return Staging{}, fail(domain.FailPreflight,
			"the destination %s has %d bytes free, %d are needed for a %d byte source, and no temp directory is configured",
			destDir, destFree, need, req.Source.SizeBytes)
	}

	tempFree, err := p.free(p.tempDir)
	if err != nil {
		return Staging{}, err
	}

	if tempFree < need {
		return Staging{}, fail(domain.FailPreflight,
			"neither the destination %s (%d bytes free) nor the temp directory %s (%d bytes free) has the %d bytes needed for a %d byte source",
			destDir, destFree, p.tempDir, tempFree, need, req.Source.SizeBytes)
	}

	return Staging{Path: filepath.Join(p.tempDir, name), FinalPath: onDest, UsedTempDir: true}, nil
}

func (p *Promoter) free(dir string) (uint64, error) {
	space, err := p.fs.Statfs(dir)
	if err != nil {
		return 0, wrap(domain.FailPreflight, err, "free space on %s could not be read", dir)
	}

	return space.FreeBytes, nil
}

// crossDevice is plan.md 15.6. NFSv4 presents every export inside one
// pseudo-filesystem, so a dataset split leaves the client-side paths looking
// unchanged while rename() silently starts returning EXDEV. Compare the numbers
// rather than trusting the paths.
func (p *Promoter) crossDevice(stagingDir, destDir string) (bool, error) {
	staging, err := p.fs.Stat(stagingDir)
	if err != nil {
		return false, wrap(domain.FailPreflight, err, "the staging directory %s could not be stat'd", stagingDir)
	}

	dest, err := p.fs.Stat(destDir)
	if err != nil {
		return false, wrap(domain.FailPreflight, err, "the destination directory %s could not be stat'd", destDir)
	}

	return staging.Device != dest.Device, nil
}

func ext(e string) string {
	if e == "" || strings.HasPrefix(e, ".") {
		return e
	}

	return "." + e
}
