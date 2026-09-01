import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { EmptyState } from '../ui/EmptyState';
import { Panel } from '../ui/Panel';
import { formatDuration, humanise } from '../../lib/format';
import { planKindTone } from '../../lib/tone';
import type { JobSummary } from '../../api/types';

interface QueuePanelProps {
  queue: JobSummary[];
  depth: number;
  paused: boolean;
  busy: boolean;
  onTogglePause: () => void;
  onOpen: (job: JobSummary) => void;
}

export function QueuePanel({ queue, depth, paused, busy, onTogglePause, onOpen }: QueuePanelProps) {
  return (
    <Panel
      title={`Queue (${depth})`}
      icon="list"
      actions={
        <Button variant="secondary" icon={paused ? 'play' : 'pause'} loading={busy} onClick={onTogglePause}>
          {paused ? 'Resume' : 'Pause'}
        </Button>
      }
    >
      {paused && (
        <p className="mb-3 rounded-lg border border-amber-800 bg-amber-950/60 px-3 py-2 text-xs text-amber-200">
          The queue is paused. A running job continues to completion; nothing new starts.
        </p>
      )}

      {queue.length === 0 ? (
        <EmptyState message="Queue is empty." />
      ) : (
        <ol className="divide-y divide-slate-800">
          {queue.map((job, index) => (
            <li key={job.id}>
              <button
                onClick={() => onOpen(job)}
                className="flex w-full items-center gap-3 py-2.5 text-left hover:bg-slate-800/50"
              >
                <span className="w-6 flex-shrink-0 text-right text-xs text-slate-500">{index + 1}</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-slate-200">{job.media_filename}</span>
                </span>
                {job.attempt > 1 && (
                  <Badge tone="warning" title="Auto-requeued after an interruption">
                    Attempt {job.attempt}
                  </Badge>
                )}
                <Badge tone={planKindTone(job.kind)}>{humanise(job.kind)}</Badge>
                <span className="w-16 flex-shrink-0 text-right text-xs text-slate-400">
                  {formatDuration(job.estimated_seconds)}
                </span>
              </button>
            </li>
          ))}
        </ol>
      )}
    </Panel>
  );
}
