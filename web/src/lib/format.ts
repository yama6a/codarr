import type { PlanKind } from '../api/types';
const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

export function formatBytes(bytes: number, digits = 1): string {
  const sign = bytes < 0 ? '-' : '';
  let value = Math.abs(bytes);
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${sign}${unit === 0 ? value : value.toFixed(digits)} ${UNITS[unit]}`;
}

/** formatSignedBytes keeps the sign visible, because bytes_saved is negative when AV1 goes to HEVC. */
export function formatSignedBytes(bytes: number, digits = 1): string {
  return bytes > 0 ? `+${formatBytes(bytes, digits)}` : formatBytes(bytes, digits);
}

export function formatDuration(seconds: number | undefined | null): string {
  if (seconds === undefined || seconds === null || !Number.isFinite(seconds)) {
    return 'unknown';
  }
  const total = Math.max(0, Math.round(seconds));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) {
    return `${h}h ${String(m).padStart(2, '0')}m`;
  }
  if (m > 0) {
    return `${m}m ${String(s).padStart(2, '0')}s`;
  }
  return `${s}s`;
}

export function formatHours(seconds: number): string {
  return `${(seconds / 3600).toFixed(1)} h`;
}

export function formatDateTime(iso: string | undefined | null): string {
  if (!iso) {
    return 'never';
  }
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return iso;
  }
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

/** formatDateParts splits a timestamp into a date line and a time line; "never" comes back with no time. */
export function formatDateParts(iso: string | undefined | null): [string, string | null] {
  if (!iso) {
    return ['never', null];
  }
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return [iso, null];
  }
  return [
    date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: '2-digit' }),
    date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }),
  ];
}

export function formatTime(iso: string | undefined | null): string {
  if (!iso) {
    return '';
  }
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return iso;
  }
  return date.toLocaleTimeString(undefined, { hour12: false });
}

export function elapsedSeconds(since: string | undefined | null, now = Date.now()): number {
  if (!since) {
    return 0;
  }
  const started = new Date(since).getTime();
  if (Number.isNaN(started)) {
    return 0;
  }
  return Math.max(0, (now - started) / 1000);
}

export function formatPercent(pct: number | undefined | null, digits = 1): string {
  if (pct === undefined || pct === null || !Number.isFinite(pct)) {
    return 'n/a';
  }
  return `${pct.toFixed(digits)}%`;
}

export function formatBitrate(kbps: number | undefined | null): string {
  if (kbps === undefined || kbps === null) {
    return 'calculating';
  }
  if (kbps >= 1000) {
    return `${(kbps / 1000).toFixed(1)} Mbps`;
  }
  return `${kbps} kbps`;
}

export function formatResolution(width?: number, height?: number): string {
  if (!width || !height) {
    return 'unknown';
  }
  return `${width}x${height}`;
}

export function deltaPercent(before: number, after: number): number | null {
  if (before <= 0) {
    return null;
  }
  return ((after - before) / before) * 100;
}

const FAILURE_LABELS: Record<string, string> = {
  interrupted: 'Interrupted',
  preflight_failed: 'Preflight failed',
  probe_failed: 'Probe failed',
  ffmpeg_failed: 'ffmpeg failed',
  verification_failed: 'Verification failed',
  promote_failed: 'Promotion failed',
  internal_error: 'Internal error',
};

export function failureLabel(code: string | undefined): string {
  if (!code) {
    return 'Failed';
  }
  return FAILURE_LABELS[code] ?? code;
}

const PROVENANCE_LABELS: Record<string, string> = {
  untouched: 'Untouched',
  codarr_output: 'Codarr output',
  modified_since_transcode: 'Modified since transcode',
};

export function provenanceLabel(value: string | undefined): string {
  if (!value) {
    return 'unknown';
  }
  return PROVENANCE_LABELS[value] ?? value;
}

export function titleFromPath(path: string): string {
  const base = path.split('/').pop() ?? path;
  return base.replace(/\.[^.]+$/, '');
}

/** planKindText names a label set for prose, where an empty set reads as skipped. */
export function planKindText(kind: PlanKind | null | undefined): string {
  return kind && kind.length > 0 ? kind.join(', ') : 'skipped';
}

/** codecCounts collapses a track list into "23x subrip, 2x ass", most frequent first. */
export function codecCounts(tracks: { codec: string }[]): string {
  if (tracks.length === 0) {
    return 'none';
  }
  const counts = new Map<string, number>();
  for (const track of tracks) {
    counts.set(track.codec, (counts.get(track.codec) ?? 0) + 1);
  }
  return [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([codec, n]) => `${n}x ${codec}`)
    .join(', ');
}

/** humanise turns a snake_case enum value into something readable without a lookup table. */
export function humanise(value: string): string {
  const spaced = value.replace(/_/g, ' ');
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
