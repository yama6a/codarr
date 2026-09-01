import { useCallback, useEffect, useRef, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { Button } from '../../components/ui/Button';
import { EmptyState } from '../../components/ui/EmptyState';
import { FormField } from '../../components/ui/FormField';
import { LoadingSpinner } from '../../components/ui/LoadingSpinner';
import { Panel } from '../../components/ui/Panel';
import { TextInput } from '../../components/ui/TextInput';
import { toast } from '../../components/ui/Toast';
import { Toggle } from '../../components/ui/Toggle';
import { PathMappings } from '../../components/settings/PathMappings';
import { SecretInput } from '../../components/settings/SecretInput';
import { TestResultLine } from '../../components/settings/TestResultLine';
import type {
  PathMapping,
  PlexAuthStart,
  PlexConfig,
  PlexConfigUpdate,
  PlexLibrary,
  ResolvePathResult,
  TestResult,
} from '../../api/types';

function toUpdate(config: PlexConfig): PlexConfigUpdate {
  return {
    base_url: config.base_url,
    token: config.token,
    refresh_after: config.refresh_after,
    analyze_after: config.analyze_after,
    guard_active_streams: config.guard_active_streams,
    path_mappings: config.path_mappings,
  };
}

export default function SettingsPlex() {
  const [config, setConfig] = useState<PlexConfig | null>(null);
  const [form, setForm] = useState<PlexConfigUpdate | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const [test, setTest] = useState<TestResult | null>(null);
  const [testing, setTesting] = useState(false);

  const [libraries, setLibraries] = useState<PlexLibrary[] | null>(null);
  const [loadingLibraries, setLoadingLibraries] = useState(false);

  const [probePath, setProbePath] = useState('');
  const [resolved, setResolved] = useState<ResolvePathResult | null>(null);

  const [pin, setPin] = useState<PlexAuthStart | null>(null);
  const [pinMessage, setPinMessage] = useState('');
  const pinTimer = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  const load = useCallback(async () => {
    const next = await unwrap(api.GET('/api/plex'));
    setConfig(next);
    setForm(toUpdate(next));
  }, []);

  useEffect(() => {
    load()
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, [load]);

  useEffect(() => () => clearInterval(pinTimer.current), []);

  const set = <K extends keyof PlexConfigUpdate>(key: K, value: PlexConfigUpdate[K]) =>
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev));

  const save = async () => {
    if (!form) {
      return;
    }
    setSaving(true);
    try {
      const saved = await unwrap(api.PUT('/api/plex', { body: form }));
      setConfig(saved);
      setForm(toUpdate(saved));
      toast.success('Plex configuration saved.');
    } catch {
      // Already toasted.
    } finally {
      setSaving(false);
    }
  };

  const runTest = async () => {
    setTesting(true);
    try {
      setTest(await unwrap(api.POST('/api/plex/test')));
    } catch {
      // Already toasted.
    } finally {
      setTesting(false);
    }
  };

  const loadLibraries = async () => {
    setLoadingLibraries(true);
    try {
      setLibraries(await unwrap(api.GET('/api/plex/libraries')));
    } catch {
      // Already toasted.
    } finally {
      setLoadingLibraries(false);
    }
  };

  const resolve = async () => {
    try {
      setResolved(await unwrap(api.POST('/api/plex/resolve-path', { body: { path: probePath } })));
    } catch {
      // Already toasted.
    }
  };

  const startPin = async () => {
    try {
      const started = await unwrap(api.POST('/api/plex/auth/start'));
      setPin(started);
      setPinMessage('Waiting for the PIN to be claimed.');
      clearInterval(pinTimer.current);
      // The PIN flow is the one thing on a settings page that polls, because plex.tv only tells us
      // out of band. It stops the moment the token is stored.
      pinTimer.current = setInterval(async () => {
        try {
          const poll = await unwrap(
            api.GET('/api/plex/auth/poll/{pin_id}', { params: { path: { pin_id: started.pin_id } } }),
          );
          setPinMessage(poll.message ?? (poll.authorized ? 'Authorized.' : 'Waiting for the PIN to be claimed.'));
          if (poll.token_stored) {
            clearInterval(pinTimer.current);
            setPin(null);
            toast.success('Plex token stored.');
            await load();
          }
        } catch {
          clearInterval(pinTimer.current);
        }
      }, 3000);
    } catch {
      // Already toasted.
    }
  };

  if (loading || !form || !config) {
    return <LoadingSpinner message="Loading Plex configuration..." />;
  }

  return (
    <div className="space-y-6 p-8">
      <header className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-white">Plex</h1>
          <p className="mt-1 text-sm text-slate-400">One server. Sessions, refresh, analyze and path mapping.</p>
        </div>
        <Button icon="save" loading={saving} onClick={save}>
          Save
        </Button>
      </header>

      <Panel
        title="Server"
        icon="plex"
        actions={
          <Button variant="secondary" icon="probe" loading={testing} onClick={runTest}>
            Test
          </Button>
        }
      >
        <div className="space-y-5">
          <FormField label="Base URL">
            <TextInput
              value={form.base_url}
              onChange={(next) => set('base_url', next)}
              placeholder="http://plex:32400"
            />
          </FormField>

          <FormField
            label="Token"
            hint="Stored in plaintext, as the *arrs do with their own keys. It is never returned by the API and never logged."
          >
            <SecretInput
              label="Plex token"
              value={form.token}
              onChange={(next) => set('token', next)}
              placeholder="Paste an X-Plex-Token"
            />
          </FormField>

          <div className="rounded-lg border border-slate-800 bg-slate-900/60 p-4">
            <p className="text-sm font-medium text-white">Or claim a token through plex.tv</p>
            <p className="mt-1 text-xs text-slate-400">
              Codarr asks plex.tv for a PIN, you claim it in a browser, and the token is stored
              server-side. It is never sent back to this page.
            </p>
            {pin ? (
              <div className="mt-3 space-y-2">
                <p className="text-sm text-slate-200">
                  Code <span className="font-mono text-lg font-bold text-white">{pin.code}</span>
                </p>
                <a
                  href={pin.auth_url}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 text-sm text-blue-400 hover:underline"
                >
                  Open plex.tv to claim it
                </a>
                <p className="text-xs text-slate-400">{pinMessage}</p>
              </div>
            ) : (
              <Button variant="secondary" icon="key" className="mt-3" onClick={startPin}>
                Start PIN flow
              </Button>
            )}
          </div>

          <TestResultLine
            result={test}
            lastTestedAt={config.last_tested_at}
            lastTestResult={config.last_test_result}
          />
          <p className="text-xs text-slate-500">Client identifier: {config.client_identifier}</p>
        </div>
      </Panel>

      <Panel title="After a replacement" icon="refresh">
        <div className="space-y-5">
          <Toggle
            checked={form.refresh_after}
            onChange={(next) => set('refresh_after', next)}
            label="Partial refresh"
            description="Scan the containing directory after a file is replaced."
          />
          <Toggle
            checked={form.analyze_after}
            onChange={(next) => set('analyze_after', next)}
            label="Analyze after"
            description="Ask Plex to re-analyze the item so its stream list matches the new file."
          />
          <Toggle
            checked={form.guard_active_streams}
            onChange={(next) => set('guard_active_streams', next)}
            label="Never replace a file Plex is streaming"
            description="Defers the job until the session ends. It never skips it. On NFS a replacement mid-stream gives the reader ESTALE, not graceful continuation."
          />
        </div>
      </Panel>

      <Panel
        title="Path mappings"
        icon="folder"
        description="Codarr sees the media at one path; Plex may see the same files at another."
      >
        <div className="space-y-5">
          <PathMappings
            value={form.path_mappings}
            remoteLabel="Plex path"
            onChange={(next: PathMapping[]) => set('path_mappings', next)}
          />

          <div className="border-t border-slate-800 pt-4">
            <FormField label="Resolver" hint="Check how a local path translates before you save.">
              <div className="flex gap-2">
                <TextInput
                  value={probePath}
                  onChange={setProbePath}
                  placeholder="/media/movies/Some Film (2019)/Some Film.mkv"
                  ariaLabel="Path to resolve"
                />
                <Button variant="secondary" onClick={resolve}>
                  Resolve
                </Button>
              </div>
            </FormField>
            {resolved && (
              <div className="mt-3 rounded-lg border border-slate-800 bg-slate-900/60 p-3 font-mono text-xs">
                <p className="text-slate-400">{resolved.local_path}</p>
                <p className="mt-1 text-slate-100">{resolved.remote_path}</p>
                <p className="mt-1 font-sans text-[11px] text-slate-500">
                  {resolved.matched
                    ? `Matched mapping ${resolved.mapping_id ?? ''}`
                    : 'No mapping matched, so the path is used as it is.'}
                </p>
              </div>
            )}
          </div>
        </div>
      </Panel>

      <Panel
        title="Libraries"
        icon="library"
        actions={
          <Button variant="secondary" icon="refresh" loading={loadingLibraries} onClick={loadLibraries}>
            Load
          </Button>
        }
      >
        {libraries === null ? (
          <p className="text-sm text-slate-400">Load the section list from the server to see it here.</p>
        ) : libraries.length === 0 ? (
          <EmptyState icon="library" message="Plex reports no library sections." />
        ) : (
          <ul className="divide-y divide-slate-800">
            {libraries.map((library) => (
              <li key={library.key} className="py-2.5">
                <p className="text-sm text-slate-100">
                  {library.title} <span className="text-xs text-slate-500">({library.type}, key {library.key})</span>
                </p>
                <p className="font-mono text-xs text-slate-500">{library.locations.join(', ')}</p>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
