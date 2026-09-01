import { Badge } from '../ui/Badge';
import { Icon } from '../ui/Icon';
import { formatBitrate, formatBytes, formatResolution, humanise, provenanceLabel, titleFromPath } from '../../lib/format';
import { mediaStatusTone, planKindTone, provenanceTone } from '../../lib/tone';
import type { MediaListItem, MediaSort } from '../../api/types';

interface LibraryTableProps {
  items: MediaListItem[];
  sort: MediaSort;
  selected: Set<number>;
  allMatchingSelected: boolean;
  onSort: (sort: MediaSort) => void;
  onToggle: (id: number) => void;
  onTogglePage: (checked: boolean) => void;
  onOpen: (item: MediaListItem) => void;
}

const columns: { key: string; label: string; sort?: MediaSort }[] = [
  { key: 'title', label: 'Title', sort: 'path' },
  { key: 'instance', label: 'Instance' },
  { key: 'container', label: 'Container' },
  { key: 'video', label: 'Video', sort: 'video_bitrate' },
  { key: 'audio', label: 'Audio' },
  { key: 'subtitles', label: 'Subtitles' },
  { key: 'size', label: 'Size', sort: 'size_bytes' },
  { key: 'plan', label: 'Plan', sort: 'plan_kind' },
  { key: 'status', label: 'Status', sort: 'status' },
  { key: 'provenance', label: 'Provenance', sort: 'provenance' },
];

function nextSort(current: MediaSort, column: MediaSort): MediaSort {
  return current === column ? (`-${column}` as MediaSort) : column;
}

export function LibraryTable({
  items,
  sort,
  selected,
  allMatchingSelected,
  onSort,
  onToggle,
  onTogglePage,
  onOpen,
}: LibraryTableProps) {
  const pageChecked = items.length > 0 && items.every((item) => selected.has(item.id));

  return (
    <div className="overflow-x-auto rounded-xl border border-slate-800 bg-surface-dark">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-slate-800 text-xs tracking-wide text-slate-400 uppercase">
          <tr>
            <th className="w-10 px-3 py-3">
              <input
                type="checkbox"
                aria-label="Select every row on this page"
                checked={pageChecked || allMatchingSelected}
                onChange={(e) => onTogglePage(e.target.checked)}
                className="size-4 rounded border-slate-600 bg-slate-800 accent-blue-600"
              />
            </th>
            {columns.map((column) => (
              <th key={column.key} className="px-3 py-3 font-medium whitespace-nowrap">
                {column.sort ? (
                  <button
                    onClick={() => onSort(nextSort(sort, column.sort as MediaSort))}
                    className="flex items-center gap-1 hover:text-white"
                  >
                    {column.label}
                    {(sort === column.sort || sort === `-${column.sort}`) && (
                      <Icon name={sort.startsWith('-') ? 'chevron_down' : 'chevron_up'} size={14} />
                    )}
                  </button>
                ) : (
                  column.label
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-slate-800">
          {items.map((item) => {
            const modified = item.provenance === 'modified_since_transcode';
            return (
              <tr
                key={item.id}
                onClick={() => onOpen(item)}
                className={`cursor-pointer transition-colors hover:bg-slate-800/60 ${
                  modified ? 'border-l-4 border-l-red-500 bg-red-950/20' : ''
                }`}
              >
                <td className="px-3 py-2.5" onClick={(e) => e.stopPropagation()}>
                  <input
                    type="checkbox"
                    aria-label={`Select ${item.filename}`}
                    checked={allMatchingSelected || selected.has(item.id)}
                    disabled={allMatchingSelected}
                    onChange={() => onToggle(item.id)}
                    className="size-4 rounded border-slate-600 bg-slate-800 accent-blue-600"
                  />
                </td>
                <td className="max-w-xs px-3 py-2.5">
                  <span className="block truncate text-slate-100" title={item.path}>
                    {titleFromPath(item.filename)}
                  </span>
                </td>
                <td className="px-3 py-2.5 whitespace-nowrap text-slate-300">
                  {item.arr_instance_name ?? 'none'}
                </td>
                <td className="px-3 py-2.5 whitespace-nowrap text-slate-300">{item.container ?? 'unknown'}</td>
                <td className="px-3 py-2.5 whitespace-nowrap text-slate-300">
                  <span className="block">
                    {item.video_codec ?? 'unknown'}
                    {item.video_profile ? ` ${item.video_profile}` : ''}
                    {item.video_level ? ` L${item.video_level}` : ''}
                    {item.is_hdr && <Badge tone="warning" className="ml-1.5">HDR</Badge>}
                  </span>
                  <span className="block text-xs text-slate-500">
                    {formatResolution(item.width, item.height)}, {formatBitrate(item.video_bitrate_kbps)}
                  </span>
                </td>
                <td className="px-3 py-2.5 text-xs text-slate-300">
                  {item.audio.length === 0
                    ? 'none'
                    : item.audio.map((track) => `${track.codec} ${track.channels}ch`).join(', ')}
                </td>
                <td className="px-3 py-2.5 text-xs text-slate-300">
                  {item.subtitles.length === 0
                    ? 'none'
                    : item.subtitles.map((track) => track.codec).join(', ')}
                </td>
                <td className="px-3 py-2.5 whitespace-nowrap text-slate-300">{formatBytes(item.size_bytes)}</td>
                <td className="px-3 py-2.5">
                  {item.plan_kind ? (
                    <Badge tone={planKindTone(item.plan_kind)}>{humanise(item.plan_kind)}</Badge>
                  ) : (
                    <span className="text-xs text-slate-500">not planned</span>
                  )}
                </td>
                <td className="px-3 py-2.5">
                  <Badge tone={mediaStatusTone(item.status)}>{humanise(item.status)}</Badge>
                </td>
                <td className="px-3 py-2.5">
                  <Badge tone={provenanceTone(item.provenance)}>
                    {modified && <Icon name="shield" size={12} />}
                    {provenanceLabel(item.provenance)}
                  </Badge>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
