package api

import (
	"context"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/pkg/domain"
)

// MaxScanRateLimitFPS bounds the stat rate a scan is allowed to ask for. Zero
// means unthrottled, which is what the scanner already treats it as.
const MaxScanRateLimitFPS = 10_000

// GetSettings returns the single settings row. Nothing here changes what gets
// transcoded or to what; that is hard-coded (plan.md 3).
func (s *Server) GetSettings(
	ctx context.Context, _ gen.GetSettingsRequestObject,
) (gen.GetSettingsResponseObject, error) {
	current, err := s.store.GetSettings(ctx)
	if err != nil {
		return gen.GetSettingsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetSettings200JSONResponse(settings(current)), nil
}

// UpdateSettings replaces the row. queue_paused is deliberately not writable
// here: pausing goes through the queue endpoints so the worker is woken.
func (s *Server) UpdateSettings(
	ctx context.Context, req gen.UpdateSettingsRequestObject,
) (gen.UpdateSettingsResponseObject, error) {
	if req.Body == nil {
		return gen.UpdateSettingsdefaultJSONResponse(s.fail(ctx, badRequest("a settings body is required"))), nil
	}

	current, err := s.store.GetSettings(ctx)
	if err != nil {
		return gen.UpdateSettingsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	updated, err := applySettings(current, *req.Body)
	if err != nil {
		return gen.UpdateSettingsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	if err := s.store.UpdateSettings(ctx, updated); err != nil {
		return gen.UpdateSettingsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	stored, err := s.store.GetSettings(ctx)
	if err != nil {
		return gen.UpdateSettingsdefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.UpdateSettings200JSONResponse(settings(stored)), nil
}

func applySettings(current domain.Settings, in gen.SettingsUpdate) (domain.Settings, error) {
	tempDir, err := settingsDir(in.TempDir, "temp_dir")
	if err != nil {
		return domain.Settings{}, err
	}

	device, err := settingsDir(in.QsvDevice, "qsv_device")
	if err != nil {
		return domain.Settings{}, err
	}

	if in.ScanRateLimitFps < 0 || in.ScanRateLimitFps > MaxScanRateLimitFPS {
		return domain.Settings{}, badRequest("scan_rate_limit_fps must be between 0 and %d", MaxScanRateLimitFPS)
	}

	current.TempDir = tempDir
	current.QSVDevice = device
	current.ScanEnabled = in.ScanEnabled
	current.ScanCron = in.ScanCron
	current.ScanRateLimitFPS = in.ScanRateLimitFps
	current.PrioritiseQuickJobs = in.PrioritiseQuickJobs
	current.FullHashEnabled = in.FullHashEnabled

	return current, nil
}
