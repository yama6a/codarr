import { Button } from '../ui/Button';
import { Icon } from '../ui/Icon';
import { TextInput } from '../ui/TextInput';
import type { PathMapping } from '../../api/types';

interface PathMappingsProps {
  value: PathMapping[];
  onChange: (next: PathMapping[]) => void;
  remoteLabel: string;
}

export function PathMappings({ value, onChange, remoteLabel }: PathMappingsProps) {
  const update = (index: number, patch: Partial<PathMapping>) =>
    onChange(value.map((row, i) => (i === index ? { ...row, ...patch } : row)));

  const remove = (index: number) =>
    onChange(value.filter((_, i) => i !== index).map((row, i) => ({ ...row, sort: i })));

  const add = () => onChange([...value, { local: '', remote: '', sort: value.length }]);

  return (
    <div className="space-y-2">
      <div className="grid grid-cols-[1fr_auto_1fr_auto] items-center gap-2 text-[11px] tracking-wide text-slate-500 uppercase">
        <span>Codarr path</span>
        <span />
        <span>{remoteLabel}</span>
        <span />
      </div>
      {value.length === 0 && <p className="text-xs text-slate-500">No mappings. Paths are used as they are.</p>}
      {value.map((row, index) => (
        <div key={row.id ?? `new-${index}`} className="grid grid-cols-[1fr_auto_1fr_auto] items-center gap-2">
          <TextInput
            value={row.local}
            onChange={(next) => update(index, { local: next })}
            ariaLabel={`Local path ${index + 1}`}
            placeholder="/media/movies"
          />
          <Icon name="chevron_right" size={16} className="text-slate-600" />
          <TextInput
            value={row.remote}
            onChange={(next) => update(index, { remote: next })}
            ariaLabel={`Remote path ${index + 1}`}
            placeholder="/data/movies"
          />
          <button
            onClick={() => remove(index)}
            aria-label={`Remove mapping ${index + 1}`}
            className="rounded-lg p-2 text-slate-400 hover:bg-slate-800 hover:text-red-400"
          >
            <Icon name="trash" size={16} />
          </button>
        </div>
      ))}
      <Button variant="ghost" icon="plus" onClick={add}>
        Add mapping
      </Button>
      <p className="text-xs text-slate-500">
        Lower rows are tried after higher ones. The first match wins.
      </p>
    </div>
  );
}
