import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
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
// Keeps the DOM bounded on a long-lived tab. The list is newest first, so trimming drops the oldest rows.
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

interface Query {
  level?: EventLevel;
  category?: string;
  since_id?: number;
  before_id?: number;
  limit: number;
}

export default function Logs() {
  const [level, setLevel] = useState<EventLevel | ''>('');
  const [category, setCategory] = useState('');
  const [events, setEvents] = useState<EventItem[]>([]);
  const [hasOlder, setHasOlder] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);

  const debouncedCategory = useDebounced(category);
  const listRef = useRef<HTMLDivElement>(null);
  const atTop = useRef(true);
  const pendingScroll = useRef<number | null>(null);
  const newestId = useRef<number | undefined>(undefined);

  const fetchEvents = useCallback(
    (extra: Partial<Query>) =>
      unwrap(
        api.GET('/api/events', {
          params: {
            query: {
              level: level || undefined,
              category: debouncedCategory || undefined,
              since_id: undefined,
              before_id: undefined,
              limit: LIMIT,
              ...extra,
            },
          },
        }),
      ),
    [level, debouncedCategory],
  );

  const loadNewest = useCallback(async () => {
    const page = await fetchEvents({});
    newestId.current = page.items[0]?.id;
    setEvents(page.items);
    setHasOlder(page.has_more);
    return page;
  }, [fetchEvents]);

  // plan.md 18.6: the logs page polls GET /api/events?since_id=<newest> on the same 10s cadence and
  // prepends what arrived. More than a page of new rows means the tail is gone; start over from the top.
  const poll = useCallback(async () => {
    if (newestId.current === undefined) {
      return loadNewest();
    }
    const page = await fetchEvents({ since_id: newestId.current });
    if (page.has_more) {
      const fresh = await loadNewest();
      setHasOlder(true);
      return fresh;
    }
    if (page.items.length === 0) {
      return page;
    }
    newestId.current = page.next_since_id;
    const node = listRef.current;
    if (node && !atTop.current) {
      pendingScroll.current = node.scrollHeight - node.scrollTop;
    }
    setEvents((prev) => {
      const merged = [...[...page.items].reverse(), ...prev];
      if (merged.length > MAX_RETAINED) {
        setHasOlder(true);
        return merged.slice(0, MAX_RETAINED);
      }
      return merged;
    });
    return page;
  }, [fetchEvents, loadNewest]);

  const { refresh } = usePolling(poll);

  const mounted = useRef(false);
  useEffect(() => {
    if (!mounted.current) {
      mounted.current = true;
      return;
    }
    newestId.current = undefined;
    setEvents([]);
    setHasOlder(false);
    refresh();
  }, [level, debouncedCategory, refresh]);

  // A prepend while the user is reading further down must not move the row under the cursor.
  useLayoutEffect(() => {
    const node = listRef.current;
    if (node && pendingScroll.current !== null) {
      node.scrollTop = node.scrollHeight - pendingScroll.current;
      pendingScroll.current = null;
    }
  }, [events]);

  const loadOlder = async () => {
    const oldest = events.at(-1);
    if (!oldest) {
      return;
    }
    setLoadingOlder(true);
    try {
      const page = await fetchEvents({ before_id: oldest.id });
      setEvents((prev) => {
        const seen = new Set(prev.map((event) => event.id));
        return [...prev, ...page.items.filter((event) => !seen.has(event.id))];
      });
      setHasOlder(page.has_more);
    } catch {
      // Already toasted by the client middleware.
    } finally {
      setLoadingOlder(false);
    }
  };

  const onScroll = () => {
    const node = listRef.current;
    if (!node) {
      return;
    }
    atTop.current = node.scrollTop < 40;
  };

  const jumpToLatest = () => {
    const node = listRef.current;
    if (node) {
      atTop.current = true;
      node.scrollTop = 0;
    }
  };

  return (
    <div className="flex h-full flex-col gap-4 p-8">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-white">Logs</h1>
          <p className="mt-1 text-sm text-slate-400">
            {events.length.toLocaleString()} events held, newest first. New rows appear at the top
            every 10 seconds.
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
          <Button variant="ghost" icon="chevron_up" onClick={jumpToLatest}>
            Jump to latest
          </Button>
        </div>
      </header>

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
                <span className="flex-shrink-0 text-slate-600">
                  {formatDateTime(event.created_at)}
                </span>
                <span
                  className={`w-12 flex-shrink-0 font-semibold uppercase ${levelClasses[event.level]}`}
                >
                  {event.level}
                </span>
                <span className="w-32 flex-shrink-0 truncate text-blue-400">{event.category}</span>
                <span className={`min-w-0 break-words ${levelClasses[event.level]}`}>
                  {event.message}
                </span>
              </li>
            ))}
          </ul>
        )}
        {hasOlder && (
          <div className="flex justify-center pt-3">
            <Button variant="ghost" icon="chevron_down" loading={loadingOlder} onClick={loadOlder}>
              Load older
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
