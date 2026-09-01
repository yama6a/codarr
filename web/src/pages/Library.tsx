import { useCallback, useEffect, useMemo, useState } from 'react';
import { api, unwrap } from '../api/client';
import { LibraryFilters } from '../components/library/LibraryFilters';
import { LibraryTable } from '../components/library/LibraryTable';
import { RecheckDialog } from '../components/library/RecheckDialog';
import { SpaceSweepDialog } from '../components/library/SpaceSweepDialog';
import { describeFilter, emptyFilters, toMediaFilter, type LibraryFilterState } from '../components/library/filters';
import { MediaDetailModal } from '../components/media/MediaDetailModal';
import { Button } from '../components/ui/Button';
import { EmptyState } from '../components/ui/EmptyState';
import { LoadingSpinner } from '../components/ui/LoadingSpinner';
import { Pagination } from '../components/ui/Pagination';
import { toast } from '../components/ui/Toast';
import { useDebounced } from '../hooks/useDebounced';
import type { ArrInstance, MediaPage, MediaSort, RecheckResult, SpaceSweepPreview } from '../api/types';

const PAGE_SIZE = 50;

type Dialog = 'selected' | 'all' | 'sweep' | null;

export default function Library() {
  const [filters, setFilters] = useState<LibraryFilterState>(emptyFilters);
  const [sort, setSort] = useState<MediaSort>('-updated_at');
  const [page, setPage] = useState(1);
  const [reloadToken, setReloadToken] = useState(0);

  const [data, setData] = useState<MediaPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [instances, setInstances] = useState<ArrInstance[]>([]);

  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [allMatching, setAllMatching] = useState(false);
  const [openId, setOpenId] = useState<number | null>(null);

  const [dialog, setDialog] = useState<Dialog>(null);
  const [recheckPreview, setRecheckPreview] = useState<RecheckResult | null>(null);
  const [sweepPreview, setSweepPreview] = useState<SpaceSweepPreview | null>(null);
  const [busy, setBusy] = useState(false);

  const debouncedFilters = useDebounced(filters);
  const mediaFilter = useMemo(() => toMediaFilter(debouncedFilters), [debouncedFilters]);

  useEffect(() => {
    unwrap(api.GET('/api/arr'))
      .then(setInstances)
      .catch(() => undefined);
  }, []);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    unwrap(
      api.GET('/api/media', {
        params: { query: { ...mediaFilter, sort, page, page_size: PAGE_SIZE } },
      }),
    )
      .then((result) => {
        if (alive) {
          setData(result);
        }
      })
      .catch(() => undefined)
      .finally(() => {
        if (alive) {
          setLoading(false);
        }
      });
    return () => {
      alive = false;
    };
  }, [mediaFilter, sort, page, reloadToken]);

  const reload = useCallback(() => setReloadToken((n) => n + 1), []);

  const changeFilters = (next: LibraryFilterState) => {
    setFilters(next);
    setPage(1);
    setSelected(new Set());
    setAllMatching(false);
  };

  const toggle = (id: number) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });

  const togglePage = (checked: boolean) => {
    setAllMatching(false);
    setSelected(checked ? new Set(data?.items.map((item) => item.id) ?? []) : new Set());
  };

  const selectionCount = allMatching ? (data?.total ?? 0) : selected.size;

  const startRecheckSelected = async () => {
    setRecheckPreview(null);
    setDialog('selected');
    // plan.md 18.2: 40k files is one filter object, not 40k integers.
    const body = allMatching
      ? { confirm: false, filter: mediaFilter }
      : { confirm: false, ids: Array.from(selected) };
    try {
      setRecheckPreview(await unwrap(api.POST('/api/media/recheck-selected', { body })));
    } catch {
      setDialog(null);
    }
  };

  const startRecheckAll = async () => {
    setRecheckPreview(null);
    setDialog('all');
    try {
      setRecheckPreview(await unwrap(api.POST('/api/media/recheck-all', { body: { confirm: false } })));
    } catch {
      setDialog(null);
    }
  };

  const startSweep = async () => {
    setSweepPreview(null);
    setDialog('sweep');
    try {
      setSweepPreview(await unwrap(api.POST('/api/space-sweep/preview')));
    } catch {
      setDialog(null);
    }
  };

  const confirmRecheck = async () => {
    setBusy(true);
    try {
      const result =
        dialog === 'all'
          ? await unwrap(api.POST('/api/media/recheck-all', { body: { confirm: true } }))
          : await unwrap(
              api.POST('/api/media/recheck-selected', {
                body: allMatching
                  ? { confirm: true, filter: mediaFilter }
                  : { confirm: true, ids: Array.from(selected) },
              }),
            );
      toast.success(`Queued ${result.queued_job_ids.length} jobs.`);
      setDialog(null);
      setSelected(new Set());
      setAllMatching(false);
      reload();
    } catch {
      // Already toasted.
    } finally {
      setBusy(false);
    }
  };

  const confirmSweep = async () => {
    if (!sweepPreview) {
      return;
    }
    setBusy(true);
    try {
      const result = await unwrap(
        api.POST('/api/space-sweep/run', {
          body: {
            confirm: true,
            media_file_ids: sweepPreview.candidates.map((candidate) => candidate.media_file_id),
          },
        }),
      );
      toast.success(`Queued ${result.count} re-encodes.`);
      setDialog(null);
      reload();
    } catch {
      // Already toasted.
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4 p-8">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold text-white">Library</h1>
          <p className="mt-1 text-sm text-slate-400">
            {data ? `${data.total.toLocaleString()} files match the current filter.` : 'Loading...'}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="ghost" icon="refresh" onClick={reload}>
            Refresh
          </Button>
          <Button variant="secondary" icon="probe" onClick={startRecheckAll}>
            Re-check all
          </Button>
          <Button variant="secondary" icon="disk" onClick={startSweep}>
            Space sweep
          </Button>
        </div>
      </header>

      <LibraryFilters value={filters} instances={instances} onChange={changeFilters} />

      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-800 bg-surface-dark px-4 py-3">
        <div className="flex flex-wrap items-center gap-3 text-sm text-slate-300">
          <span>{selectionCount.toLocaleString()} selected</span>
          {data && selected.size > 0 && !allMatching && data.total > selected.size && (
            <button onClick={() => setAllMatching(true)} className="text-blue-400 hover:underline">
              Select all {data.total.toLocaleString()} matching the filter
            </button>
          )}
          {allMatching && (
            <button
              onClick={() => {
                setAllMatching(false);
                setSelected(new Set());
              }}
              className="text-blue-400 hover:underline"
            >
              Clear selection
            </button>
          )}
        </div>
        <Button
          variant="danger"
          icon="retry"
          disabled={selectionCount === 0}
          onClick={startRecheckSelected}
        >
          Re-encode selected
        </Button>
      </div>

      <p className="text-xs text-slate-500">
        Every bulk action previews first and asks for confirmation. Promotion replaces the source
        file in place: there is no trash and no undo, and re-fetching from Radarr or Sonarr is the
        only recovery path if a transcode ruins a file.
      </p>

      {loading && !data ? (
        <LoadingSpinner message="Loading library..." />
      ) : !data || data.items.length === 0 ? (
        <EmptyState icon="library" message="No files match this filter." />
      ) : (
        <>
          <LibraryTable
            items={data.items}
            sort={sort}
            selected={selected}
            allMatchingSelected={allMatching}
            onSort={(next) => {
              setSort(next);
              setPage(1);
            }}
            onToggle={toggle}
            onTogglePage={togglePage}
            onOpen={(item) => setOpenId(item.id)}
          />
          <Pagination
            total={data.total}
            limit={PAGE_SIZE}
            offset={(page - 1) * PAGE_SIZE}
            onPageChange={(offset) => setPage(Math.floor(offset / PAGE_SIZE) + 1)}
          />
        </>
      )}

      <RecheckDialog
        open={dialog === 'selected' || dialog === 'all'}
        title={dialog === 'all' ? 'Re-check every done file' : 'Re-encode selected'}
        scope={
          dialog === 'all'
            ? 'every file Codarr has already processed'
            : allMatching
              ? describeFilter(debouncedFilters)
              : `${selected.size.toLocaleString()} selected files`
        }
        preview={recheckPreview}
        busy={busy}
        onConfirm={confirmRecheck}
        onCancel={() => setDialog(null)}
      />

      <SpaceSweepDialog
        open={dialog === 'sweep'}
        preview={sweepPreview}
        busy={busy}
        onConfirm={confirmSweep}
        onCancel={() => setDialog(null)}
      />

      {openId !== null && (
        <MediaDetailModal mediaFileId={openId} onClose={() => setOpenId(null)} onChanged={reload} />
      )}
    </div>
  );
}
