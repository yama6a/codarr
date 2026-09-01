import { useEffect, useState } from 'react';

/**
 * useNow re-renders once a second so elapsed timers move between the 10s dashboard polls. It makes
 * no request; plan.md 18.6 is about network polling, not the clock.
 */
export function useNow(enabled: boolean): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!enabled) {
      return;
    }
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [enabled]);

  return now;
}
