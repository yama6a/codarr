import { Badge } from '../ui/Badge';
import { EmptyState } from '../ui/EmptyState';
import { Panel } from '../ui/Panel';
import { deltaPercent, formatBytes, formatDuration, formatSignedBytes, humanise } from '../../lib/format';
import { planKindTone } from '../../lib/tone';
import type { JobSummary } from '../../api/types';

interface CompletionsPanelProps {
  jobs: JobSummary[];
  onOpen: (job: JobSummary) => void;
}

export function CompletionsPanel({ jobs, onOpen }: CompletionsPanelProps) {
  return (
    <Panel title="Recent completions" icon="check">
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
                    <span className="min-w-0 flex-1 truncate text-sm text-slate-200">{job.media_filename}</span>
                    <Badge tone={planKindTone(job.kind)}>{humanise(job.kind)}</Badge>
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-slate-400">
                    <span>
                      {formatBytes(before)} to {formatBytes(after)}
                    </span>
                    <span className={delta <= 0 ? 'font-medium text-green-400' : 'font-medium text-red-400'}>
                      {formatSignedBytes(delta)}
                      {pct !== null ? ` (${pct > 0 ? '+' : ''}${pct.toFixed(1)}%)` : ''}
                    </span>
                    <span>took {formatDuration(job.actual_seconds)}</span>
                    {job.fell_back && <span className="font-semibold text-red-400">software fallback</span>}
                  </div>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}
