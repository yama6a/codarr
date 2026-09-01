import { act, renderHook } from '@testing-library/react';
import { usePolling } from '../usePolling';

function setHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', { configurable: true, value: hidden });
  document.dispatchEvent(new Event('visibilitychange'));
}

// testing-library's waitFor polls on real timers, so it never resolves here. Flush microtasks instead.
const flush = () => act(async () => {});

describe('usePolling', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    setHidden(false);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('polls immediately and then every 10 seconds', async () => {
    const fetcher = vi.fn().mockResolvedValue('ok');
    const { result } = renderHook(() => usePolling(fetcher));

    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(result.current.data).toBe('ok');
    expect(result.current.loading).toBe(false);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('stops while hidden and polls once on becoming visible', async () => {
    const fetcher = vi.fn().mockResolvedValue('ok');
    renderHook(() => usePolling(fetcher));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);

    act(() => setHidden(true));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(fetcher).toHaveBeenCalledTimes(1);

    act(() => setHidden(false));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('refresh() polls without waiting for the next tick', async () => {
    const fetcher = vi.fn().mockResolvedValue('ok');
    const { result } = renderHook(() => usePolling(fetcher));
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(1);

    act(() => result.current.refresh());
    await flush();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('surfaces a rejected fetch as an error', async () => {
    const fetcher = vi.fn().mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => usePolling(fetcher));

    await flush();
    expect(result.current.error).toEqual(new Error('boom'));
    expect(result.current.data).toBeNull();
    expect(result.current.loading).toBe(false);
  });
});
