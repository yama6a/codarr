import { useCallback, useEffect, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { EmptyState } from '../../components/ui/EmptyState';
import { FormField } from '../../components/ui/FormField';
import { LoadingSpinner } from '../../components/ui/LoadingSpinner';
import { Panel } from '../../components/ui/Panel';
import { Select } from '../../components/ui/Select';
import { TextInput } from '../../components/ui/TextInput';
import { toast } from '../../components/ui/Toast';
import { formatDateTime } from '../../lib/format';
import type { ArrInstance, Root } from '../../api/types';

export default function SettingsRoots() {
  const [roots, setRoots] = useState<Root[]>([]);
  const [instances, setInstances] = useState<ArrInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [path, setPath] = useState('');
  const [owner, setOwner] = useState('');
  const [adding, setAdding] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);

  const load = useCallback(async () => {
    const [nextRoots, nextInstances] = await Promise.all([
      unwrap(api.GET('/api/roots')),
      unwrap(api.GET('/api/arr')),
    ]);
    setRoots(nextRoots);
    setInstances(nextInstances);
  }, []);

  useEffect(() => {
    load()
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, [load]);

  const add = async () => {
    setAdding(true);
    try {
      await unwrap(
        api.POST('/api/roots', {
          body: {
            path,
            arr_instance_id: owner ? Number(owner) : null,
            enabled: true,
          },
        }),
      );
      setPath('');
      setOwner('');
      toast.success('Root added.');
      await load();
    } catch {
      // Already toasted.
    } finally {
      setAdding(false);
    }
  };

  const remove = async (root: Root) => {
    setBusyId(root.id);
    try {
      await unwrap(api.DELETE('/api/roots/{id}', { params: { path: { id: root.id } } }));
      toast.success('Root removed. Its media rows survive with no root.');
      await load();
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  const scan = async (root: Root) => {
    setBusyId(root.id);
    try {
      await unwrap(api.POST('/api/roots/{id}/scan', { params: { path: { id: root.id } } }));
      toast.success('Scan started in the background.');
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  if (loading) {
    return <LoadingSpinner message="Loading roots..." />;
  }

  const ownerOptions = [
    { value: '', label: 'No instance' },
    ...instances.map((instance) => ({ value: String(instance.id), label: instance.name })),
  ];

  return (
    <div className="space-y-6 p-8">
      <header>
        <h1 className="text-2xl font-bold text-white">Roots</h1>
        <p className="mt-1 text-sm text-slate-400">
          Every tree Codarr walks. Most come from an instance's root folders; add the rest by hand.
        </p>
      </header>

      <Panel title="Add a root" icon="plus">
        <div className="grid gap-4 sm:grid-cols-[2fr_1fr_auto] sm:items-end">
          <FormField label="Path">
            <TextInput value={path} onChange={setPath} placeholder="/media/movies" />
          </FormField>
          <FormField
            label="Owning instance"
            hint="A root with no instance is processed, but nothing is notified afterwards."
          >
            <Select
              ariaLabel="Owning instance"
              value={owner}
              options={ownerOptions}
              onChange={setOwner}
              className="w-full"
            />
          </FormField>
          <Button icon="plus" loading={adding} disabled={path.trim() === ''} onClick={add}>
            Add
          </Button>
        </div>
      </Panel>

      <Panel title={`Watch roots (${roots.length})`} icon="folder">
        {roots.length === 0 ? (
          <EmptyState icon="folder" message="No roots yet. Import them from an instance or add one above." />
        ) : (
          <ul className="divide-y divide-slate-800">
            {roots.map((root) => (
              <li key={root.id} className="flex flex-wrap items-center gap-3 py-3">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-sm text-slate-100">{root.path}</p>
                  <p className="mt-0.5 text-xs text-slate-500">
                    {root.arr_instance_name ?? 'no instance'}, {(root.media_file_count ?? 0).toLocaleString()} files,
                    added {formatDateTime(root.created_at)}
                  </p>
                </div>
                {root.imported && <Badge tone="info">imported</Badge>}
                <Badge tone={root.enabled ? 'success' : 'neutral'}>
                  {root.enabled ? 'Enabled' : 'Disabled'}
                </Badge>
                <Button variant="ghost" icon="probe" loading={busyId === root.id} onClick={() => scan(root)}>
                  Scan now
                </Button>
                <Button variant="ghost" icon="trash" loading={busyId === root.id} onClick={() => remove(root)}>
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
