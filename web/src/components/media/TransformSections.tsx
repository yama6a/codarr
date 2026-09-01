import { CompareHeader, CompareRow } from './CompareRow';
import { Badge } from '../ui/Badge';
import { Icon } from '../ui/Icon';
import {
  deltaPercent,
  formatBitrate,
  formatBytes,
  formatDuration,
  formatResolution,
  formatSignedBytes,
} from '../../lib/format';
import type { AudioState, SubtitleState, TransformRecord, VideoState } from '../../api/types';

function VideoCell({ state }: { state?: VideoState }) {
  if (!state) {
    return <span className="text-slate-500">not present</span>;
  }
  return (
    <ul className="space-y-0.5">
      <li>
        {state.codec} {state.profile && `(${state.profile}${state.level ? ` L${state.level}` : ''})`}
      </li>
      <li>
        {formatResolution(state.width, state.height)}, {state.fps ? `${state.fps.toFixed(3)} fps` : 'unknown fps'}
      </li>
      <li>{formatBitrate(state.bitrate_kbps)}</li>
      <li>
        {state.pix_fmt || 'unknown pix_fmt'}, {state.scan}
        {state.hdr && <span className="ml-1 text-amber-300">HDR</span>}
      </li>
    </ul>
  );
}

function AudioCell({ state }: { state?: AudioState }) {
  if (!state) {
    return <span className="text-slate-500">dropped</span>;
  }
  return (
    <ul className="space-y-0.5">
      <li>
        {state.codec}
        {state.profile ? ` (${state.profile})` : ''}
      </li>
      <li>
        {state.channels} ch, {state.layout || 'unknown layout'}
      </li>
      <li>{formatBitrate(state.bitrate_kbps)}</li>
    </ul>
  );
}

function SubtitleCell({ state }: { state?: SubtitleState }) {
  if (!state) {
    return <span className="text-slate-500">dropped</span>;
  }
  return (
    <span>
      {state.codec}
      {state.forced ? ', forced' : ''}
    </span>
  );
}

interface TransformSectionsProps {
  transform: TransformRecord;
  produced: boolean;
}

// One schema for both halves of a job's life. plan.md 18.3 insists the `after` column says which
// it is: the plan, or what ffprobe measured on the real output.
export function TransformSections({ transform, produced }: TransformSectionsProps) {
  const afterLabel = produced ? 'After (produced)' : 'After (planned)';
  const afterHint = produced
    ? 'Measured by ffprobe on the file Codarr promoted'
    : 'The planned target. Nothing has been written yet.';

  const sizeDelta = transform.size.after_bytes - transform.size.before_bytes;
  const sizePct = deltaPercent(transform.size.before_bytes, transform.size.after_bytes);

  return (
    <div className="space-y-6">
      <div
        className={`flex items-center gap-2 rounded-lg border px-3 py-2 text-xs ${
          produced ? 'border-green-800 bg-green-950/50 text-green-200' : 'border-sky-800 bg-sky-950/50 text-sky-200'
        }`}
      >
        <Icon name={produced ? 'check' : 'clock'} size={16} />
        {produced
          ? 'This job finished. The after column is what was actually produced.'
          : 'This job has not produced output yet. The after column is the plan.'}
      </div>

      <section>
        <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Container</h3>
        <CompareHeader afterLabel={afterLabel} afterHint={afterHint} />
        <CompareRow
          label="Container"
          before={transform.container.before}
          after={transform.container.after}
        />
        <CompareRow
          label="Attachments"
          before={String(transform.attachments.before)}
          after={String(transform.attachments.after)}
        />
        <CompareRow
          label="Chapters"
          before={String(transform.chapters.before)}
          after={String(transform.chapters.after)}
        />
      </section>

      {transform.video && (
        <section>
          <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Video</h3>
          <CompareRow
            label="Video stream"
            action={transform.video.action}
            reason={transform.video.reason}
            before={<VideoCell state={transform.video.before} />}
            after={<VideoCell state={transform.video.after} />}
          />
        </section>
      )}

      <section>
        <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">
          Audio ({transform.audio.length})
        </h3>
        {transform.audio.length === 0 ? (
          <p className="text-xs text-slate-500">No audio streams.</p>
        ) : (
          transform.audio.map((track) => (
            <CompareRow
              key={`${track.source_index}`}
              label={`Track ${track.source_index}${track.language ? ` (${track.language})` : ''}`}
              sublabel={`${track.title ?? 'untitled'}, output ${track.output_index ?? 'none'}`}
              action={track.action}
              reason={track.reason}
              before={<AudioCell state={track.before} />}
              after={<AudioCell state={track.after} />}
            />
          ))
        )}
      </section>

      <section>
        <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">
          Subtitles ({transform.subtitles.length})
        </h3>
        {transform.subtitles.length === 0 ? (
          <p className="text-xs text-slate-500">No subtitle streams.</p>
        ) : (
          transform.subtitles.map((track) => (
            <CompareRow
              key={`${track.source_index}`}
              label={`Track ${track.source_index}${track.language ? ` (${track.language})` : ''}`}
              sublabel={`output ${track.output_index ?? 'none'}`}
              action={track.action}
              reason={track.reason}
              before={<SubtitleCell state={track.before} />}
              after={<SubtitleCell state={track.after} />}
            />
          ))
        )}
      </section>

      <section>
        <h3 className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Size and duration</h3>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border border-slate-800 bg-slate-900/70 p-3">
            <p className="text-[11px] tracking-wide text-slate-500 uppercase">File size</p>
            <p className="mt-1 text-sm text-slate-200">
              {formatBytes(transform.size.before_bytes)} to {formatBytes(transform.size.after_bytes)}
            </p>
            <p className={`mt-0.5 text-xs font-medium ${sizeDelta <= 0 ? 'text-green-400' : 'text-red-400'}`}>
              {formatSignedBytes(sizeDelta)}
              {sizePct !== null ? ` (${sizePct > 0 ? '+' : ''}${sizePct.toFixed(1)}%)` : ''}
              {!produced && <span className="ml-1 text-slate-500">projected</span>}
            </p>
          </div>
          <div className="rounded-lg border border-slate-800 bg-slate-900/70 p-3">
            <p className="text-[11px] tracking-wide text-slate-500 uppercase">Job duration</p>
            <p className="mt-1 text-sm text-slate-200">
              estimated {formatDuration(transform.duration_seconds.estimated)}
            </p>
            <p className="mt-0.5 text-xs text-slate-400">
              {transform.duration_seconds.actual === null || transform.duration_seconds.actual === undefined ? (
                <Badge tone="neutral">actual pending</Badge>
              ) : (
                `actual ${formatDuration(transform.duration_seconds.actual)}`
              )}
            </p>
          </div>
        </div>
      </section>
    </div>
  );
}
