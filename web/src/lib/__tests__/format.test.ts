import {
  deltaPercent,
  elapsedSeconds,
  failureLabel,
  formatBitrate,
  formatBytes,
  formatDuration,
  formatSignedBytes,
  humanise,
  provenanceLabel,
  titleFromPath,
} from '../format';

describe('formatBytes', () => {
  it('scales through the units', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(1536)).toBe('1.5 KB');
    expect(formatBytes(10_000_000_000)).toBe('9.3 GB');
  });

  it('keeps the sign on a negative total', () => {
    expect(formatBytes(-1536)).toBe('-1.5 KB');
  });
});

describe('formatSignedBytes', () => {
  // bytes_saved is negative when an AV1 source is re-encoded to HEVC, which is by design.
  it('marks growth and shrinkage differently', () => {
    expect(formatSignedBytes(1536)).toBe('+1.5 KB');
    expect(formatSignedBytes(-1536)).toBe('-1.5 KB');
    expect(formatSignedBytes(0)).toBe('0 B');
  });
});

describe('formatDuration', () => {
  it('formats hours, minutes and seconds', () => {
    expect(formatDuration(45)).toBe('45s');
    expect(formatDuration(125)).toBe('2m 05s');
    expect(formatDuration(7325)).toBe('2h 02m');
  });

  it('reports an absent duration rather than zero', () => {
    expect(formatDuration(null)).toBe('unknown');
    expect(formatDuration(undefined)).toBe('unknown');
  });
});

describe('formatBitrate', () => {
  it('says calculating when the sample probe has not run', () => {
    expect(formatBitrate(null)).toBe('calculating');
    expect(formatBitrate(800)).toBe('800 kbps');
    expect(formatBitrate(12000)).toBe('12.0 Mbps');
  });
});

describe('deltaPercent', () => {
  it('returns null when there is nothing to divide by', () => {
    expect(deltaPercent(0, 100)).toBeNull();
    expect(deltaPercent(100, 60)).toBe(-40);
  });
});

describe('elapsedSeconds', () => {
  it('measures from an RFC 3339 instant', () => {
    const start = '2026-08-01T10:00:00.000000000Z';
    expect(elapsedSeconds(start, Date.parse(start) + 90_000)).toBe(90);
    expect(elapsedSeconds(null)).toBe(0);
  });
});

describe('labels', () => {
  it('renders failure codes readably', () => {
    expect(failureLabel('ffmpeg_failed')).toBe('ffmpeg failed');
    expect(failureLabel('interrupted')).toBe('Interrupted');
    expect(failureLabel(undefined)).toBe('Failed');
  });

  it('renders provenance readably', () => {
    expect(provenanceLabel('modified_since_transcode')).toBe('Modified since transcode');
  });

  it('humanises snake case', () => {
    expect(humanise('audio_only')).toBe('Audio only');
  });

  it('strips the extension for a title', () => {
    expect(titleFromPath('/media/movies/Arrival (2016)/Arrival.mkv')).toBe('Arrival');
  });
});
