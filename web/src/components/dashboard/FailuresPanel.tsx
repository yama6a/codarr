import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { EmptyState } from '../ui/EmptyState';
import { Panel } from '../ui/Panel';
import { failureLabel, formatDateTime } from '../../lib/format';
import type { JobSummary } from '../../api/types';

interface FailuresPanelProps {
  jobs: JobSummary[];
  retryingId: number | null;
  onRetry: (jobId: number) => void;
  onOpen: (job: JobSummary) => void;
}

export function FailuresPanel({ jobs, retryingId, onRetry, onOpen }: FailuresPanelProps) {
  return (
    <Panel title={`Failures (${jobs.length})`} icon="error">
      {jobs.length === 0 ? (
        <EmptyState icon="success" message="No failures need attention." />
      ) : (
        <ul className="divide-y divide-slate-800">
          {jobs.map((job) => (
            <li key={job.id} className="flex items-start gap-3 py-2.5">
              <button onClick={() => onOpen(job)} className="min-w-0 flex-1 text-left">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 truncate text-sm text-slate-200">{job.media_filename}</span>
                  <Badge tone={job.failure_code === 'interrupted' ? 'warning' : 'danger'}>
                    {failureLabel(job.failure_code)}
                  </Badge>
                </div>
                {job.failure_message && (
                  <p className="mt-1 line-clamp-2 text-xs text-red-300">{job.failure_message}</p>
                )}
                <p className="mt-0.5 text-[11px] text-slate-500">{formatDateTime(job.finished_at)}</p>
              </button>
              <Button
                variant="secondary"
                icon="retry"
                loading={retryingId === job.id}
                onClick={() => onRetry(job.id)}
              >
                Retry
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
