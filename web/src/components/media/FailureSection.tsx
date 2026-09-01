import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { Icon } from '../ui/Icon';
import { failureLabel } from '../../lib/format';
import type { Job } from '../../api/types';

interface FailureSectionProps {
  job: Job;
  retrying: boolean;
  onRetry: () => void;
}

export function FailureSection({ job, retrying, onRetry }: FailureSectionProps) {
  const interrupted = job.failure_code === 'interrupted';

  return (
    <div className="space-y-3 rounded-lg border border-red-800 bg-red-950/40 p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Icon name="error" size={20} className="text-red-400" />
          <span className="text-sm font-semibold text-red-100">{failureLabel(job.failure_code)}</span>
          <Badge tone={interrupted ? 'warning' : 'danger'}>{job.failure_code ?? 'unknown'}</Badge>
          {job.attempt > 1 && <Badge tone="warning">Attempt {job.attempt}</Badge>}
        </div>
        <Button variant="secondary" icon="retry" loading={retrying} onClick={onRetry}>
          Retry
        </Button>
      </div>

      {interrupted && (
        <p className="text-xs text-amber-200">
          Codarr restarted while this job was running, so it was interrupted rather than failing on
          its own. Nothing was promoted and the source file is untouched. Retrying starts it again
          from the beginning.
        </p>
      )}

      {job.failure_message && <p className="text-sm break-words text-red-100">{job.failure_message}</p>}

      {job.stderr_tail && (
        <div>
          <p className="mb-1 text-[11px] font-medium tracking-wide text-slate-400 uppercase">ffmpeg stderr tail</p>
          <pre className="max-h-64 overflow-auto rounded-lg bg-slate-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-red-300">
            {job.stderr_tail}
          </pre>
        </div>
      )}
    </div>
  );
}
