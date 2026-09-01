import { useCallback, useEffect, useRef, useState } from 'react';

// plan.md 18.6. Not configurable: ten seconds is comfortable resolution for work measured in minutes.
const INTERVAL_MS = 10_000;

export interface Polling<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  refresh: () => void;
}

// Call `refresh()` straight after a mutation rather than waiting for the next tick.
export function usePolling<T>(fetcher: () => Promise<T>): Polling<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const [loading, setLoading] = useState(true);

  // Held in a ref so an inline arrow from the caller does not restart the interval on every render.
  const fetcherRef = useRef(fetcher);
  useEffect(() => {
    fetcherRef.current = fetcher;
  });

  const aliveRef = useRef(true);
  const timerRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);
  // Discards the response of a poll that a later poll has already overtaken.
  const seqRef = useRef(0);

  const poll = useCallback(async () => {
    const seq = ++seqRef.current;
    try {
      const result = await fetcherRef.current();
      if (aliveRef.current && seq === seqRef.current) {
        setData(result);
        setError(null);
      }
    } catch (err) {
      if (aliveRef.current && seq === seqRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (aliveRef.current && seq === seqRef.current) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    aliveRef.current = true;

    const stop = () => {
      clearInterval(timerRef.current);
      timerRef.current = undefined;
    };

    const start = () => {
      stop();
      void poll();
      timerRef.current = setInterval(() => void poll(), INTERVAL_MS);
    };

    const onVisibilityChange = () => {
      if (document.hidden) {
        stop();
      } else {
        start();
      }
    };

    if (document.hidden) {
      setLoading(false);
    } else {
      start();
    }
    document.addEventListener('visibilitychange', onVisibilityChange);

    return () => {
      aliveRef.current = false;
      stop();
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [poll]);

  const refresh = useCallback(() => void poll(), [poll]);

  return { data, error, loading, refresh };
}
