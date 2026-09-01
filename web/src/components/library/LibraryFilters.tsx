import { Button } from '../ui/Button';
import { SearchInput } from '../ui/SearchInput';
import { Select } from '../ui/Select';
import { TextInput } from '../ui/TextInput';
import { emptyFilters, type LibraryFilterState } from './filters';
import type { ArrInstance } from '../../api/types';

interface LibraryFiltersProps {
  value: LibraryFilterState;
  instances: ArrInstance[];
  onChange: (next: LibraryFilterState) => void;
}

const planKinds = [
  { value: '', label: 'Any plan' },
  { value: 'skip', label: 'Skip' },
  { value: 'remux', label: 'Remux' },
  { value: 'audio_only', label: 'Audio only' },
  { value: 'full', label: 'Full' },
];

const statuses = [
  { value: '', label: 'Any status' },
  { value: 'new', label: 'New' },
  { value: 'analyzed', label: 'Analyzed' },
  { value: 'queued', label: 'Queued' },
  { value: 'processing', label: 'Processing' },
  { value: 'done', label: 'Done' },
  { value: 'failed', label: 'Failed' },
  { value: 'ignored', label: 'Ignored' },
  { value: 'skipped', label: 'Skipped' },
  { value: 'missing', label: 'Missing' },
];

const provenances = [
  { value: '', label: 'Any provenance' },
  { value: 'untouched', label: 'Untouched' },
  { value: 'codarr_output', label: 'Codarr output' },
  { value: 'modified_since_transcode', label: 'Modified since transcode' },
];

export function LibraryFilters({ value, instances, onChange }: LibraryFiltersProps) {
  const set = <K extends keyof LibraryFilterState>(key: K, next: LibraryFilterState[K]) =>
    onChange({ ...value, [key]: next });

  const instanceOptions = [
    { value: '', label: 'Any instance' },
    ...instances.map((instance) => ({ value: String(instance.id), label: instance.name })),
  ];

  return (
    <div className="space-y-3 rounded-xl border border-slate-800 bg-surface-dark p-4">
      <div className="grid gap-3 lg:grid-cols-[2fr_1fr_1fr]">
        <SearchInput
          value={value.q}
          onChange={(next) => set('q', next)}
          placeholder="Search the path..."
        />
        <TextInput
          value={value.video_codec}
          onChange={(next) => set('video_codec', next)}
          placeholder="Video codec, e.g. h264"
          ariaLabel="Video codec"
        />
        <Select
          ariaLabel="Source instance"
          value={value.arr_instance_id}
          options={instanceOptions}
          onChange={(next) => set('arr_instance_id', next)}
        />
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Select
          ariaLabel="Plan kind"
          value={value.plan_kind}
          options={planKinds}
          onChange={(next) => set('plan_kind', next as LibraryFilterState['plan_kind'])}
        />
        <Select
          ariaLabel="Status"
          value={value.status}
          options={statuses}
          onChange={(next) => set('status', next as LibraryFilterState['status'])}
        />
        <Select
          ariaLabel="Provenance"
          value={value.provenance}
          options={provenances}
          onChange={(next) => set('provenance', next as LibraryFilterState['provenance'])}
        />
        <button
          onClick={() => onChange({ ...emptyFilters, provenance: 'modified_since_transcode' })}
          className="rounded-lg border border-red-800 bg-red-950/60 px-3 py-2 text-xs font-medium text-red-300 transition-colors hover:bg-red-900/60"
        >
          Show files changed after Codarr wrote them
        </button>
        <Button variant="ghost" icon="close" onClick={() => onChange(emptyFilters)}>
          Clear
        </Button>
      </div>
    </div>
  );
}
