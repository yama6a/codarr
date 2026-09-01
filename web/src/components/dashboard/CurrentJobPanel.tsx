import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { EmptyState } from '../ui/EmptyState';
import { KeyValue } from '../ui/StatTile';
import { Panel } from '../ui/Panel';
import { ProgressBar } from '../ui/ProgressBar';
import { FallbackWarning } from './FallbackWarning';
import { useNow } from '../../hooks/useNow';
import { elapsedSeconds, formatBytes, formatDuration, formatPercent, humanise } from '../../lib/format';
import { jobStateTone, planKindTone } from '../../lib/tone';
import type { JobSummary } from '../../api/types';

interface CurrentJobPanelProps {
  job?: JobSummary;
  paused: boolean;
  onCancel: (jobId: number) => void;
  onOpen: (job: JobSummary) => void;
  cancelling: boolean;
}

function remainingSeconds(job: JobSummary, elapsed: number): number | null {
  const pct = job.progress_pct ?? 0;
  if (pct > 1 && pct < 100 && elapsed > 0) {
    return (elapsed * (100 - pct)) / pct;
  }
  if (job.estimated_seconds) {
    return Math.max(0, job.estimated_seconds - elapsed);
  }
  return null;
}

export function CurrentJobPanel({ job, paused, onCancel, onOpen, cancelling }: CurrentJobPanelProps) {
  const now = useNow(job !== undefined);

  if (!job) {
    return (
      <Panel title="Current job" icon="play">
        <EmptyState
          icon="inbox"
          message={paused ? 'Queue is paused. Nothing is running.' : 'Nothing is running right now.'}
        />
      </Panel>
    );
  }

  const elapsed = elapsedSeconds(job.started_at, now);
  const remaining = remainingSeconds(job, elapsed);

  return (
    <Panel
      title="Current job"
      icon="play"
      actions={
        <Button variant="danger" icon="ban" loading={cancelling} onClick={() => onCancel(job.id)}>
          Cancel
        </Button>
      }
    >
      <div className="space-y-4">
        <FallbackWarning job={job} />

        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={() => onOpen(job)}
            className="truncate text-left text-base font-semibold text-white hover:text-blue-300 hover:underline"
          >
            {job.media_filename}
          </button>
          <Badge tone={planKindTone(job.kind)}>{humanise(job.kind)}</Badge>
          <Badge tone={jobStateTone(job.state)}>{humanise(job.state)}</Badge>
          {job.attempt > 1 && <Badge tone="warning">Attempt {job.attempt}</Badge>}
        </div>

        <p className="truncate font-mono text-xs text-slate-500">{job.media_path}</p>

        <div>
          <div className="mb-1.5 flex items-baseline justify-between text-sm">
            <span className="font-medium text-slate-200">{formatPercent(job.progress_pct ?? 0)}</span>
            <span className="text-xs text-slate-400">
              {formatDuration(elapsed)} elapsed
              {remaining !== null ? `, ${formatDuration(remaining)} remaining` : ''}
            </span>
          </div>
          <ProgressBar pct={job.progress_pct ?? 0} tone={job.fell_back ? 'warning' : 'primary'} label="Encode progress" />
        </div>

        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <KeyValue label="Speed">
            {job.progress_speed ? `${job.progress_speed.toFixed(2)}x realtime` : 'n/a'}
          </KeyValue>
          <KeyValue label="Encoder">
            <span className={job.fell_back ? 'font-semibold text-red-400' : ''}>
              {job.encoder_used ?? 'not started'}
            </span>
          </KeyValue>
          <KeyValue label="Decode path">
            <span className={job.decode_path === 'software' ? 'text-amber-400' : ''}>
              {job.decode_path ?? 'not started'}
            </span>
          </KeyValue>
          <KeyValue label="Estimated total">{formatDuration(job.estimated_seconds)}</KeyValue>
          <KeyValue label="Source size">{job.source_size ? formatBytes(job.source_size) : 'unknown'}</KeyValue>
          <KeyValue label="Origin">{humanise(job.origin)}</KeyValue>
          <KeyValue label="Job">#{job.id}</KeyValue>
          <KeyValue label="Blocked by">{job.blocked_by || 'nothing'}</KeyValue>
        </dl>
      </div>
    </Panel>
  );
}
