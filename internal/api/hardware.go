package api

import (
	"context"

	gen "github.com/yama6a/codarr/api"
	"github.com/yama6a/codarr/internal/decide"
	"github.com/yama6a/codarr/internal/hardware"
)

// GetHardware serves the cached probe. plan.md 10.1 says "at startup and on
// demand", but re-probing on every read burns six ffmpeg invocations for an
// answer already in SQLite, so the read is cache-first and only the button
// forces a fresh run.
func (s *Server) GetHardware(
	ctx context.Context, _ gen.GetHardwareRequestObject,
) (gen.GetHardwareResponseObject, error) {
	caps, err := s.hardware.Capabilities(ctx)
	if err != nil {
		return gen.GetHardwaredefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.GetHardware200JSONResponse(hardwareView(caps)), nil
}

// ProbeHardware re-runs the whole matrix now, replacing the cache.
func (s *Server) ProbeHardware(
	ctx context.Context, _ gen.ProbeHardwareRequestObject,
) (gen.ProbeHardwareResponseObject, error) {
	caps, err := s.hardware.Probe(ctx)
	if err != nil {
		return gen.ProbeHardwaredefaultJSONResponse(s.fail(ctx, err)), nil
	}

	return gen.ProbeHardware200JSONResponse(hardwareView(caps)), nil
}

func hardwareView(caps hardware.Capabilities) gen.Hardware {
	entries := make([]gen.HWCapability, 0, len(caps.Entries))
	for _, c := range caps.Entries {
		entries = append(entries, hwCapability(c))
	}

	out := gen.Hardware{
		Capabilities:         entries,
		FfmpegVersion:        strPtr(caps.FfmpegVersion),
		HardwareDecodeCodecs: decide.HardwareDecodeCodecs(),
		QsvDevice:            caps.Device,
		Remediation:          strPtr(caps.Remediation()),
	}

	if !caps.ProbedAt.IsZero() {
		out.ProbedAt = ptrOf(caps.ProbedAt)
	}

	if caps.Probed() {
		out.SelectedEncoder = ptrOf(gen.Encoder(caps.Select(false).Encoder))
	}

	return out
}
