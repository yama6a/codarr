import type {
  ArrInstance,
  Dashboard,
  Job,
  JobSummary,
  MediaDetail,
  MediaListItem,
  MediaPage,
  TransformRecord,
} from '../src/api/types';

export function jobSummary(overrides: Partial<JobSummary> = {}): JobSummary {
  return {
    id: 1,
    media_file_id: 10,
    media_path: '/media/movies/Arrival (2016)/Arrival.mkv',
    media_filename: 'Arrival.mkv',
    kind: 'full',
    origin: 'ingest',
    priority: 100,
    state: 'running',
    attempt: 1,
    fell_back: false,
    queued_at: '2026-08-01T10:00:00.000000000Z',
    started_at: '2026-08-01T10:01:00.000000000Z',
    progress_pct: 42.5,
    progress_speed: 3.2,
    estimated_seconds: 1800,
    encoder_used: 'hevc_qsv',
    decode_path: 'hardware',
    source_size: 10_000_000_000,
    ...overrides,
  };
}

export function transformRecord(overrides: Partial<TransformRecord> = {}): TransformRecord {
  return {
    container: { before: 'matroska', after: 'matroska' },
    video: {
      action: 'encode',
      reason: 'H.264 High L5.1 exceeds the copy level',
      before: {
        codec: 'h264',
        profile: 'High',
        level: '5.1',
        width: 3840,
        height: 2160,
        fps: 23.976,
        bitrate_kbps: 42000,
        pix_fmt: 'yuv420p',
        hdr: false,
        scan: 'progressive',
      },
      after: {
        codec: 'hevc',
        profile: 'Main',
        level: '5.1',
        width: 3840,
        height: 2160,
        fps: 23.976,
        bitrate_kbps: 12000,
        pix_fmt: 'yuv420p',
        hdr: false,
        scan: 'progressive',
      },
    },
    audio: [
      {
        source_index: 1,
        output_index: 0,
        language: 'eng',
        title: 'Surround',
        action: 'copy',
        reason: 'EAC3 5.1 is on the copy list',
        before: { codec: 'eac3', channels: 6, layout: '5.1', bitrate_kbps: 768 },
        after: { codec: 'eac3', channels: 6, layout: '5.1', bitrate_kbps: 768 },
      },
    ],
    subtitles: [
      {
        source_index: 2,
        output_index: null,
        language: 'eng',
        action: 'drop',
        reason: 'PGS forces a burn-in transcode',
        before: { codec: 'hdmv_pgs_subtitle', forced: false },
      },
    ],
    attachments: { before: 2, after: 0 },
    chapters: { before: 12, after: 12 },
    size: { before_bytes: 10_000_000_000, after_bytes: 4_000_000_000 },
    duration_seconds: { estimated: 1800, actual: null },
    ...overrides,
  };
}

export function job(overrides: Partial<Job> = {}): Job {
  return {
    id: 1,
    media_file_id: 10,
    media_path: '/media/movies/Arrival (2016)/Arrival.mkv',
    media_filename: 'Arrival.mkv',
    kind: 'full',
    origin: 'ingest',
    priority: 100,
    state: 'running',
    attempt: 1,
    fell_back: false,
    used_temp_dir: false,
    transform: transformRecord(),
    queued_at: '2026-08-01T10:00:00.000000000Z',
    ffmpeg_argv: ['ffmpeg', '-i', 'in.mkv', 'out.mkv'],
    ...overrides,
  };
}

export function mediaListItem(overrides: Partial<MediaListItem> = {}): MediaListItem {
  return {
    id: 10,
    path: '/media/movies/Arrival (2016)/Arrival.mkv',
    filename: 'Arrival.mkv',
    size_bytes: 10_000_000_000,
    is_hdr: false,
    audio: [{ codec: 'eac3', channels: 6, layout: '5.1', language: 'eng' }],
    subtitles: [{ codec: 'subrip', language: 'eng', forced: false }],
    status: 'analyzed',
    provenance: 'untouched',
    ignored: false,
    codarr_tagged: false,
    updated_at: '2026-08-01T10:00:00.000000000Z',
    container: 'matroska',
    video_codec: 'h264',
    video_profile: 'High',
    video_level: '5.1',
    width: 3840,
    height: 2160,
    video_bitrate_kbps: 42000,
    plan_kind: 'full',
    arr_instance_name: 'radarr-4k',
    ...overrides,
  };
}

export function mediaPage(items: MediaListItem[], total = items.length): MediaPage {
  return { items, total, page: 1, page_size: 50 };
}

export function mediaDetail(overrides: Partial<MediaDetail> = {}): MediaDetail {
  return {
    id: 10,
    path: '/media/movies/Arrival (2016)/Arrival.mkv',
    filename: 'Arrival.mkv',
    size_bytes: 10_000_000_000,
    mtime: 1_754_000_000,
    is_hdr: false,
    audio: [{ codec: 'eac3', channels: 6, layout: '5.1', language: 'eng' }],
    subtitles: [],
    status: 'done',
    provenance: 'codarr_output',
    ignored: false,
    codarr_tagged: true,
    plan_reasons: ['video encode: level too high'],
    created_at: '2026-07-01T10:00:00.000000000Z',
    updated_at: '2026-08-01T10:00:00.000000000Z',
    arr_instance_name: 'radarr-4k',
    container: 'matroska',
    fingerprint: 'xxh3-128:aaaa',
    fingerprint_algo: 'xxh3-128',
    codarr_output_fingerprint: 'xxh3-128:aaaa',
    latest_job_id: 1,
    ...overrides,
  };
}

export function dashboard(overrides: Partial<Dashboard> = {}): Dashboard {
  return {
    generated_at: '2026-08-01T10:05:00.000000000Z',
    queue_paused: false,
    queue_depth: 1,
    queue: [jobSummary({ id: 2, state: 'queued', attempt: 2, kind: 'remux', estimated_seconds: 120 })],
    awaiting_stream_end: [],
    recent_completions: [],
    failures: [],
    stats: {
      files_total: 100,
      files_done: 40,
      files_pending: 55,
      files_failed: 2,
      files_skipped: 1,
      files_ignored: 1,
      files_missing: 1,
      jobs_done: 40,
      jobs_failed: 2,
      bytes_in: 1_000_000_000,
      bytes_out: 1_200_000_000,
      bytes_saved: -200_000_000,
      encode_seconds: 36_000,
    },
    compatibility: {
      files_analyzed: 90,
      files_compatible: 60,
      files_needing_work: 30,
      files_unanalyzed: 10,
      by_reason: { audio: 20, subtitles: 12, video: 5, container: 1 },
      by_plan_kind: { skip: 60, remux: 10, audio_only: 15, full: 5 },
    },
    ...overrides,
  };
}

export function arrInstance(overrides: Partial<ArrInstance> = {}): ArrInstance {
  return {
    id: 1,
    name: 'radarr-4k',
    flavour: 'radarr',
    base_url: 'http://radarr:7878',
    api_key: '********',
    webhook_id: 'abc123',
    rescan_after: true,
    unmonitor_after: false,
    enabled: true,
    path_mappings: [],
    created_at: '2026-07-01T10:00:00.000000000Z',
    updated_at: '2026-07-01T10:00:00.000000000Z',
    ...overrides,
  };
}
