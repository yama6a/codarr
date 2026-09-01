import { useCallback, useEffect, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { ArrInstanceEditor, type ArrEditorValue } from '../../components/settings/ArrInstanceEditor';
import { TestResultLine } from '../../components/settings/TestResultLine';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { EmptyState } from '../../components/ui/EmptyState';
import { Icon } from '../../components/ui/Icon';
import { LoadingSpinner } from '../../components/ui/LoadingSpinner';
import { Panel } from '../../components/ui/Panel';
import { toast } from '../../components/ui/Toast';
import { formatBytes } from '../../lib/format';
import type { ArrInstance, ArrRootFolder, ContestedRoot, ImportRootsResult, TestResult } from '../../api/types';

function webhookUrl(webhookId: string): string {
  return `${window.location.origin}/api/webhook/${webhookId}`;
}

export default function SettingsArr() {
  const [instances, setInstances] = useState<ArrInstance[]>([]);
  const [conflicts, setConflicts] = useState<ContestedRoot[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<ArrInstance | null | undefined>(undefined);
  const [saving, setSaving] = useState(false);
  const [busyId, setBusyId] = useState<number | null>(null);
  const [tests, setTests] = useState<Record<number, TestResult>>({});
  const [rootFolders, setRootFolders] = useState<Record<number, ArrRootFolder[]>>({});
  const [imports, setImports] = useState<Record<number, ImportRootsResult>>({});

  const load = useCallback(async () => {
    const [nextInstances, nextRoots] = await Promise.all([
      unwrap(api.GET('/api/arr')),
      unwrap(api.GET('/api/roots')),
    ]);
    setInstances(nextInstances);
    setConflicts(nextRoots.conflicts);
  }, []);

  useEffect(() => {
    load()
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, [load]);

  const save = async (value: ArrEditorValue) => {
    setSaving(true);
    try {
      if (value.id === undefined) {
        await unwrap(
          api.POST('/api/arr', {
            body: {
              name: value.name,
              flavour: value.flavour,
              base_url: value.base_url,
              api_key: value.api_key,
              rescan_after: value.rescan_after,
              unmonitor_after: value.unmonitor_after,
              enabled: value.enabled,
              path_mappings: value.path_mappings,
            },
          }),
        );
        toast.success('Instance added.');
      } else {
        await unwrap(
          api.PUT('/api/arr/{id}', {
            params: { path: { id: value.id } },
            body: {
              name: value.name,
              flavour: value.flavour,
              base_url: value.base_url,
              api_key: value.api_key,
              rescan_after: value.rescan_after,
              unmonitor_after: value.unmonitor_after,
              enabled: value.enabled,
              path_mappings: value.path_mappings,
            },
          }),
        );
        toast.success('Instance saved.');
      }
      setEditing(undefined);
      await load();
    } catch {
      // Already toasted.
    } finally {
      setSaving(false);
    }
  };

  const toggleEnabled = async (instance: ArrInstance) => {
    setBusyId(instance.id);
    try {
      await unwrap(
        api.PUT('/api/arr/{id}', {
          params: { path: { id: instance.id } },
          body: {
            name: instance.name,
            flavour: instance.flavour,
            base_url: instance.base_url,
            // The mask is sent straight back, which leaves the stored key untouched (plan.md 18.4).
            api_key: instance.api_key,
            rescan_after: instance.rescan_after,
            unmonitor_after: instance.unmonitor_after,
            enabled: !instance.enabled,
            path_mappings: instance.path_mappings,
          },
        }),
      );
      await load();
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  const remove = async (instance: ArrInstance) => {
    setBusyId(instance.id);
    try {
      await unwrap(api.DELETE('/api/arr/{id}', { params: { path: { id: instance.id } } }));
      toast.success(`${instance.name} deleted. Its roots keep their files but nothing is notified.`);
      await load();
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  const test = async (instance: ArrInstance) => {
    setBusyId(instance.id);
    try {
      const result = await unwrap(api.POST('/api/arr/{id}/test', { params: { path: { id: instance.id } } }));
      setTests((prev) => ({ ...prev, [instance.id]: result }));
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  const listRootFolders = async (instance: ArrInstance) => {
    setBusyId(instance.id);
    try {
      const result = await unwrap(
        api.GET('/api/arr/{id}/rootfolders', { params: { path: { id: instance.id } } }),
      );
      setRootFolders((prev) => ({ ...prev, [instance.id]: result }));
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  const importRoots = async (instance: ArrInstance) => {
    setBusyId(instance.id);
    try {
      const result = await unwrap(
        api.POST('/api/arr/{id}/import-roots', { params: { path: { id: instance.id } } }),
      );
      setImports((prev) => ({ ...prev, [instance.id]: result }));
      toast.success(`Imported ${result.imported}, skipped ${result.skipped}.`);
      await load();
    } catch {
      // Already toasted.
    } finally {
      setBusyId(null);
    }
  };

  if (loading) {
    return <LoadingSpinner message="Loading instances..." />;
  }

  return (
    <div className="space-y-6 p-8">
      <header className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-white">Radarr and Sonarr</h1>
          <p className="mt-1 text-sm text-slate-400">
            As many instances as you run. Each owns its own roots, mappings and webhook.
          </p>
        </div>
        <Button icon="plus" onClick={() => setEditing(null)}>
          Add instance
        </Button>
      </header>

      {conflicts.length > 0 && (
        <div role="alert" className="rounded-xl border-2 border-red-500 bg-red-950 p-4">
          <p className="flex items-center gap-2 text-sm font-bold text-red-100">
            <Icon name="alert" size={18} className="text-red-400" />
            Two enabled instances claim the same root
          </p>
          <ul className="mt-2 space-y-1 text-xs text-red-200">
            {conflicts.map((conflict) => (
              <li key={conflict.path}>
                <span className="font-mono">{conflict.path}</span> is claimed by{' '}
                {conflict.instances.map((instance) => instance.name || `instance ${instance.id}`).join(' and ')}
              </li>
            ))}
          </ul>
          <p className="mt-2 text-xs text-red-300">
            Codarr never guesses an owner. Files under a contested root are still processed, but no
            instance is notified when one is replaced, so its library goes stale. Disable one
            instance or narrow its roots.
          </p>
        </div>
      )}

      {instances.length === 0 ? (
        <EmptyState icon="hub" message="No instances configured yet." />
      ) : (
        instances.map((instance) => (
          <Panel
            key={instance.id}
            title={instance.name}
            icon="hub"
            actions={
              <>
                <Button variant="ghost" icon="probe" loading={busyId === instance.id} onClick={() => test(instance)}>
                  Test
                </Button>
                <Button variant="ghost" icon="edit" onClick={() => setEditing(instance)}>
                  Edit
                </Button>
                <Button variant="ghost" icon="trash" onClick={() => remove(instance)}>
                  Delete
                </Button>
              </>
            }
          >
            <div className="space-y-4">
              <div className="flex flex-wrap items-center gap-2">
                <Badge tone="info">{instance.flavour}</Badge>
                <Badge tone={instance.enabled ? 'success' : 'neutral'}>
                  {instance.enabled ? 'Enabled' : 'Disabled'}
                </Badge>
                <span className="font-mono text-xs text-slate-400">{instance.base_url}</span>
                <button
                  onClick={() => toggleEnabled(instance)}
                  className="text-xs text-blue-400 hover:underline"
                >
                  {instance.enabled ? 'Disable' : 'Enable'}
                </button>
              </div>

              <TestResultLine
                result={tests[instance.id]}
                lastTestedAt={instance.last_tested_at}
                lastTestResult={instance.last_test_result}
              />

              <div>
                <p className="text-[11px] font-medium tracking-wide text-slate-500 uppercase">Webhook URL</p>
                <div className="mt-1 flex items-center gap-2">
                  <code className="flex-1 truncate rounded-lg border border-slate-800 bg-slate-900 px-3 py-2 text-xs text-slate-200">
                    {webhookUrl(instance.webhook_id)}
                  </code>
                  <Button
                    variant="secondary"
                    icon="copy"
                    onClick={() => {
                      void navigator.clipboard?.writeText(webhookUrl(instance.webhook_id));
                      toast.success('Webhook URL copied.');
                    }}
                  >
                    Copy
                  </Button>
                </div>
                <p className="mt-1 text-xs text-slate-500">
                  Paste this into the instance under Settings, Connect, Webhook. On Grab is not
                  needed; On Import, On Upgrade and On Rename are.
                </p>
              </div>

              <div>
                <p className="text-[11px] font-medium tracking-wide text-slate-500 uppercase">Path mappings</p>
                {instance.path_mappings.length === 0 ? (
                  <p className="mt-1 text-xs text-slate-500">None. Paths are used as the instance reports them.</p>
                ) : (
                  <ul className="mt-1 space-y-0.5 font-mono text-xs text-slate-300">
                    {instance.path_mappings.map((mapping) => (
                      <li key={mapping.id ?? `${mapping.local}-${mapping.remote}`}>
                        {mapping.local} to {mapping.remote}
                      </li>
                    ))}
                  </ul>
                )}
              </div>

              <div className="flex flex-wrap gap-2 border-t border-slate-800 pt-4">
                <Button
                  variant="secondary"
                  icon="folder"
                  loading={busyId === instance.id}
                  onClick={() => listRootFolders(instance)}
                >
                  Show root folders
                </Button>
                <Button
                  variant="secondary"
                  icon="plus"
                  loading={busyId === instance.id}
                  onClick={() => importRoots(instance)}
                >
                  Import root folders
                </Button>
              </div>

              {rootFolders[instance.id] && (
                <ul className="space-y-1 text-xs">
                  {rootFolders[instance.id].map((folder) => (
                    <li key={folder.id} className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-slate-300">{folder.path}</span>
                      <span className="text-slate-500">maps to</span>
                      <span className="font-mono text-slate-300">{folder.local_path}</span>
                      {!folder.accessible && <Badge tone="danger">not accessible</Badge>}
                      {folder.already_imported && <Badge tone="neutral">already imported</Badge>}
                      {folder.free_space !== null && folder.free_space !== undefined && (
                        <span className="text-slate-500">{formatBytes(folder.free_space)} free</span>
                      )}
                    </li>
                  ))}
                </ul>
              )}

              {imports[instance.id] && imports[instance.id].conflicts.length > 0 && (
                <div className="rounded-lg border border-red-800 bg-red-950/50 p-3 text-xs text-red-200">
                  <p className="font-semibold">Skipped, already claimed by another enabled instance:</p>
                  <ul className="mt-1 space-y-0.5">
                    {imports[instance.id].conflicts.map((conflict) => (
                      <li key={conflict.path}>
                        <span className="font-mono">{conflict.path}</span> is owned by{' '}
                        {conflict.owning_arr_instance_name}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          </Panel>
        ))
      )}

      {editing !== undefined && (
        <ArrInstanceEditor
          instance={editing}
          saving={saving}
          onSave={save}
          onCancel={() => setEditing(undefined)}
        />
      )}
    </div>
  );
}
