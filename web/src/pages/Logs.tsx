import { useCallback, useEffect, useRef, useState } from 'react';
import { api, unwrap } from '../api/client';
import { Button } from '../components/ui/Button';
import { EmptyState } from '../components/ui/EmptyState';
import { Select } from '../components/ui/Select';
import { TextInput } from '../components/ui/TextInput';
import { useDebounced } from '../hooks/useDebounced';
import { usePolling } from '../hooks/usePolling';
import { formatDateTime } from '../lib/format';
import type { EventItem, EventLevel } from '../api/types';

const LIMIT = 200;
// Keeps the DOM bounded on a long-lived tab. The cursor still moves forward; only the tail is kept.
const MAX_RETAINED = 2000;

const levels = [
  { value: '', label: 'All levels' },
  { value: 'debug', label: 'Debug and above' },
  { value: 'info', label: 'Info and above' },
  { value: 'warn', label: 'Warn and above' },
  { value: 'error', label: 'Error only' },
];

const levelClasses: Record<EventLevel, string> = {
  debug: 'text-slate-500',
  info: 'text-slate-300',
  warn: 'text-amber-300',
  error: 'text-red-400',
};

export default function Logs() {
  const [level, setLevel] = useState<EventLevel | ''>('');
  const [category, setCategory] = useState('');
  const [events, setEvents] = useState<EventItem[]>([]);
  const [hasMore, setHasMore] = useState(false);

  const debouncedCategory = useDebounced(category);
  const sinceId = useRef<number | undefined>(undefined);
  const listRef = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);

  const fetchPage = useCallback(async () => {
    const page = await unwrap(
      api.GET('/api/events', {
        params: {
          query: {
            level: level || undefined,
            category: debouncedCategory || undefined,
            since_id: sinceId.current,
            limit: LIMIT,
          },
        },
      }),
    );
    sinceId.current = page.next_since_id;
    setHasMore(page.has_more);
    setEvents((prev) => {
      // Ascending by id, so anything at or below the tail is a replay of a page already held.
      const lastId = prev.length > 0 ? prev[prev.length - 1].id : 0;
      const fresh = page.items.filter((event) => event.id > lastId);
      return fresh.length > 0 ? [...prev, ...fresh].slice(-MAX_RETAINED) : prev;
    });
    return page;
  }, [level, debouncedCategory]);

  // plan.md 18.6: the logs page polls GET /api/events?since_id=<last> on the same 10s cadence.
  const { refresh } = usePolling(fetchPage);

  const mounted = useRef(false);
  useEffect(() => {
    if (!mounted.current) {
      mounted.current = true;
      return;
    }
    sinceId.current = undefined;
    setEvents([]);
    refresh();
  }, [level, debouncedCategory, refresh]);

  // Freeze the view when the user has scrolled up; resume following once they are back at the bottom.
  useEffect(() => {
    const node = listRef.current;
    if (node && atBottom.current) {
      node.scrollTop = node.scrollHeight;
    }
  }, [events]);

  const onScroll = () => {
    const node = listRef.current;
    if (!node) {
      return;
    }
    atBottom.current = node.scrollHeight - node.scrollTop - node.clientHeight < 40;
  };

  const jumpToBottom = () => {
    const node = listRef.current;
    if (node) {
      atBottom.current = true;
      node.scrollTop = node.scrollHeight;
    }
  };

  return (
    <div className="flex h-full flex-col gap-4 p-8">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-white">Logs</h1>
          <p className="mt-1 text-sm text-slate-400">
            {events.length.toLocaleString()} events held. Following the tail while you stay scrolled
            to the bottom.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Select
            ariaLabel="Minimum level"
            value={level}
            options={levels}
            onChange={(next) => setLevel(next as EventLevel | '')}
          />
          <TextInput
            value={category}
            onChange={setCategory}
            placeholder="Category"
            ariaLabel="Category"
            className="w-44"
          />
          <Button variant="ghost" icon="chevron_down" onClick={jumpToBottom}>
            Jump to latest
          </Button>
        </div>
      </header>

      {hasMore && (
        <div className="flex items-center justify-between rounded-lg border border-amber-800 bg-amber-950/50 px-3 py-2 text-xs text-amber-200">
          <span>More events matched than one page holds. The next poll continues from the cursor.</span>
          <Button variant="ghost" icon="refresh" onClick={refresh}>
            Fetch now
          </Button>
        </div>
      )}

      <div
        ref={listRef}
        onScroll={onScroll}
        className="min-h-0 flex-1 overflow-y-auto rounded-xl border border-slate-800 bg-surface-dark p-3 font-mono text-xs"
      >
        {events.length === 0 ? (
          <EmptyState icon="logs" message="No events yet." />
        ) : (
          <ul className="space-y-0.5">
            {events.map((event) => (
              <li key={event.id} className="flex gap-3 rounded px-2 py-1 hover:bg-slate-800/50">
                <span className="flex-shrink-0 text-slate-600">{formatDateTime(event.created_at)}</span>
                <span className={`w-12 flex-shrink-0 font-semibold uppercase ${levelClasses[event.level]}`}>
                  {event.level}
                </span>
                <span className="w-32 flex-shrink-0 truncate text-blue-400">{event.category}</span>
                <span className={`min-w-0 break-words ${levelClasses[event.level]}`}>{event.message}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
