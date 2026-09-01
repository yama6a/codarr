import { EmptyState } from '../ui/EmptyState';
import { Panel } from '../ui/Panel';
import { formatDuration } from '../../lib/format';
import type { AwaitingStreamEnd } from '../../api/types';

interface AwaitingPanelProps {
  items: AwaitingStreamEnd[];
  onOpen: (item: AwaitingStreamEnd) => void;
}

function blockedBy(item: AwaitingStreamEnd): string {
  const who = item.session_user || 'a Plex session';
  const player = item.session_player ? ` on ${item.session_player}` : '';
  return `${who}${player}`;
}

export function AwaitingPanel({ items, onOpen }: AwaitingPanelProps) {
  return (
    <Panel
      title={`Awaiting stream end (${items.length})`}
      icon="plex"
      description="Deferred, never skipped. Replacing a file Plex is reading gives the client ESTALE, not a graceful continuation."
    >
      {items.length === 0 ? (
        <EmptyState icon="plex" message="Nothing is waiting on a Plex stream." />
      ) : (
        <ul className="divide-y divide-slate-800">
          {items.map((item) => (
            <li key={item.job_id}>
              <button
                onClick={() => onOpen(item)}
                className="flex w-full items-center gap-3 py-2.5 text-left hover:bg-slate-800/50"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm text-slate-200">{item.filename}</span>
                  <span className="block truncate text-xs text-amber-300">Blocked by {blockedBy(item)}</span>
                </span>
                <span className="flex-shrink-0 text-right text-xs text-slate-400">
                  waiting {formatDuration(item.waiting_seconds)}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
