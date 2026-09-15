import { Badge } from '../ui/Badge';
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
import type { Completion } from '../../api/types';

interface CompletionsPanelProps {
  items: Completion[];
  total: number;
  loadingMore: boolean;
  onLoadMore: () => void;
  onOpen: (item: Completion) => void;
}

function JobDetails({ item }: { item: Completion }) {
  const before = item.source_size ?? 0;
  const after = item.output_size ?? 0;
  const delta = after - before;
  const pct = deltaPercent(before, after);
  return (
    <>
      <span>
        {formatBytes(before)} to {formatBytes(after)}
      </span>
      <span className={delta <= 0 ? 'font-medium text-green-400' : 'font-medium text-red-400'}>
        {formatSignedBytes(delta)}
        {pct !== null ? ` (${pct > 0 ? '+' : ''}${pct.toFixed(1)}%)` : ''}
      </span>
      <span>took {formatDuration(item.actual_seconds)}</span>
    </>
  );
}

export function CompletionsPanel({
  items,
  total,
  loadingMore,
  onLoadMore,
  onOpen,
}: CompletionsPanelProps) {
  const title =
    total > items.length
      ? `Completions (latest ${items.length} of ${total})`
      : `Completions (${items.length})`;
  return (
    <Panel title={title} icon="check">
      {items.length === 0 ? (
        <EmptyState icon="check" message="Nothing finished yet." />
      ) : (
        <ul className="divide-y divide-slate-800">
          {items.map((item) => (
            <li key={item.job_id ? `job-${item.job_id}` : `media-${item.media_file_id}`}>
              <button
                onClick={() => onOpen(item)}
                className="w-full py-2.5 text-left hover:bg-slate-800/50"
              >
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm text-slate-200">
                    {item.media_filename}
                  </span>
                  {item.skipped ? (
                    <Badge tone="neutral" title="Every stream already matches the policy">
                      Skipped
                    </Badge>
                  ) : (
                    <PlanKindBadges kind={item.kind} />
                  )}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 text-xs text-slate-400">
                  {item.skipped ? <span>nothing to do</span> : <JobDetails item={item} />}
                  <span className="text-slate-500">{formatDateTime(item.at)}</span>
                  {item.fell_back && (
                    <span className="font-semibold text-red-400">software fallback</span>
                  )}
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}
      <LoadMore shown={items.length} total={total} loading={loadingMore} onLoadMore={onLoadMore} />
    </Panel>
  );
}
