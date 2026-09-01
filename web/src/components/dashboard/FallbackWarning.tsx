import { Icon } from '../ui/Icon';
import type { JobSummary } from '../../api/types';

// Deliberately the loudest thing on the page. plan.md 10.2: a silent software fallback turns a
// 20-minute job into a 4-hour one, and nothing else on the dashboard says so.
export function FallbackWarning({ job }: { job: Pick<JobSummary, 'fell_back' | 'fallback_reason' | 'encoder_used'> }) {
  if (!job.fell_back) {
    return null;
  }

  return (
    <div
      role="alert"
      className="flex items-start gap-3 rounded-lg border-2 border-red-500 bg-red-950 p-4 text-red-100"
    >
      <Icon name="alert" size={22} className="mt-0.5 flex-shrink-0 text-red-400" />
      <div>
        <p className="text-sm font-bold tracking-wide text-red-200 uppercase">
          Software encoder fallback
        </p>
        <p className="mt-1 text-sm">
          Running on {job.encoder_used ?? 'a software encoder'} instead of the hardware encoder. This
          job will take roughly an order of magnitude longer than it should.
        </p>
        {job.fallback_reason && <p className="mt-1 text-xs text-red-300">{job.fallback_reason}</p>}
      </div>
    </div>
  );
}
