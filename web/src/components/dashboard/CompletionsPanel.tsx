import { EmptyState } from '../ui/EmptyState';
import { Panel } from '../ui/Panel';
import { PlanKindBadges } from '../ui/PlanKindBadges';
import { LoadMore } from './LoadMore';
import {
  deltaPercent,
  formatBytes,
  formatDateTime,
  formatDuration,
  formatSignedBytes,
} from '../../lib/format';
import type { JobSummary } from '../../api/types';

interface CompletionsPanelProps {
  jobs: JobSummary[];
  total: number;
  loadingMore: boolean;
  onLoadMore: () => void;
  onOpen: (job: JobSummary) => void;
}

export function CompletionsPanel({
  jobs,
  total,
  loadingMore,
  onLoadMore,
  onOpen,
}: CompletionsPanelProps) {
  const title =
    total > jobs.length
      ? `Completions (latest ${jobs.length} of ${total})`
      : `Completions (${jobs.length})`;
  return (
    <Panel title={title} icon="check">
      {jobs.length === 0 ? (
        <EmptyState icon="check" message="Nothing finished yet." />
      ) : (
        <ul className="divide-y divide-slate-800">
          {jobs.map((job) => {
            const before = job.source_size ?? 0;
            const after = job.output_size ?? 0;
            const delta = after - before;
            const pct = deltaPercent(before, after);
            return (
              <li key={job.id}>
                <button
                  onClick={() => onOpen(job)}
                  className="w-full py-2.5 text-left hover:bg-slate-800/50"
                >
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate text-sm text-slate-200">
                      {job.media_filename}
                    </span>
                    <PlanKindBadges kind={job.kind} />
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-slate-400">
                    <span>
                      {formatBytes(before)} to {formatBytes(after)}
                    </span>
                    <span
                      className={
                        delta <= 0 ? 'font-medium text-green-400' : 'font-medium text-red-400'
                      }
                    >
                      {formatSignedBytes(delta)}
                      {pct !== null ? ` (${pct > 0 ? '+' : ''}${pct.toFixed(1)}%)` : ''}
                    </span>
                    <span>took {formatDuration(job.actual_seconds)}</span>
                    <span className="text-slate-500">{formatDateTime(job.finished_at)}</span>
                    {job.fell_back && (
                      <span className="font-semibold text-red-400">software fallback</span>
                    )}
                  </div>
                </button>
              </li>
            );
          })}
        </ul>
      )}
      <LoadMore shown={jobs.length} total={total} loading={loadingMore} onLoadMore={onLoadMore} />
    </Panel>
  );
}
