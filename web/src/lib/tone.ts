import type { BadgeTone } from '../components/ui/Badge';
import type { Decision, JobState, MediaStatus, PlanKind, Provenance } from '../api/types';

export function planKindTone(kind: PlanKind | undefined): BadgeTone {
  switch (kind) {
    case 'skip':
      return 'neutral';
    case 'remux':
      return 'info';
    case 'audio_only':
      return 'accent';
    case 'full':
      return 'warning';
    default:
      return 'neutral';
  }
}

export function jobStateTone(state: JobState | undefined): BadgeTone {
  switch (state) {
    case 'done':
      return 'success';
    case 'failed':
      return 'danger';
    case 'cancelled':
      return 'neutral';
    case 'awaiting_stream_end':
      return 'warning';
    case 'running':
    case 'verifying':
    case 'promoting':
      return 'info';
    default:
      return 'neutral';
  }
}

export function mediaStatusTone(status: MediaStatus | undefined): BadgeTone {
  switch (status) {
    case 'done':
      return 'success';
    case 'failed':
    case 'missing':
      return 'danger';
    case 'processing':
    case 'queued':
      return 'info';
    case 'ignored':
    case 'skipped':
      return 'neutral';
    default:
      return 'neutral';
  }
}

// modified_since_transcode is the view that surfaces something quietly undoing Codarr's work
// (plan.md 18.2), so it never shares a colour with the two benign values.
export function provenanceTone(provenance: Provenance | undefined): BadgeTone {
  switch (provenance) {
    case 'modified_since_transcode':
      return 'danger';
    case 'codarr_output':
      return 'success';
    default:
      return 'neutral';
  }
}

export function decisionTone(decision: Decision | undefined): BadgeTone {
  switch (decision) {
    case 'copy':
      return 'success';
    case 'encode':
      return 'warning';
    case 'convert':
      return 'info';
    case 'drop':
      return 'danger';
    default:
      return 'neutral';
  }
}
