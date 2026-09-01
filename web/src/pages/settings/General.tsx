import { useEffect, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { Button } from '../../components/ui/Button';
import { FormField } from '../../components/ui/FormField';
import { LoadingSpinner } from '../../components/ui/LoadingSpinner';
import { Panel } from '../../components/ui/Panel';
import { TextInput } from '../../components/ui/TextInput';
import { toast } from '../../components/ui/Toast';
import { Toggle } from '../../components/ui/Toggle';
import type { Settings, SettingsUpdate } from '../../api/types';

function toUpdate(settings: Settings): SettingsUpdate {
  return {
    temp_dir: settings.temp_dir,
    qsv_device: settings.qsv_device,
    scan_enabled: settings.scan_enabled,
    scan_cron: settings.scan_cron,
    scan_rate_limit_fps: settings.scan_rate_limit_fps,
    prioritise_quick_jobs: settings.prioritise_quick_jobs,
    full_hash_enabled: settings.full_hash_enabled,
  };
}

export default function SettingsGeneral() {
  const [form, setForm] = useState<SettingsUpdate | null>(null);
  const [paused, setPaused] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [pausing, setPausing] = useState(false);

  useEffect(() => {
    unwrap(api.GET('/api/settings'))
      .then((settings) => {
        setForm(toUpdate(settings));
        setPaused(settings.queue_paused);
      })
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, []);

  const set = <K extends keyof SettingsUpdate>(key: K, value: SettingsUpdate[K]) =>
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev));

  const save = async () => {
    if (!form) {
      return;
    }
    setSaving(true);
    try {
      const saved = await unwrap(api.PUT('/api/settings', { body: form }));
      setForm(toUpdate(saved));
      setPaused(saved.queue_paused);
      toast.success('Settings saved.');
    } catch {
      // Already toasted.
    } finally {
      setSaving(false);
    }
  };

  const toggleQueue = async () => {
    setPausing(true);
    try {
      const state = await unwrap(paused ? api.POST('/api/queue/resume') : api.POST('/api/queue/pause'));
      setPaused(state.paused);
    } catch {
      // Already toasted.
    } finally {
      setPausing(false);
    }
  };

  if (loading || !form) {
    return <LoadingSpinner message="Loading settings..." />;
  }

  return (
    <div className="space-y-6 p-8">
      <header className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-white">General</h1>
          <p className="mt-1 text-sm text-slate-400">
            There is no config file. Everything Codarr can be told is edited here.
          </p>
        </div>
        <Button icon="save" loading={saving} onClick={save}>
          Save
        </Button>
      </header>

      <Panel title="Paths and device" icon="hardware">
        <div className="space-y-5">
          <p className="rounded-lg border border-amber-800 bg-amber-950/40 px-3 py-2 text-xs text-amber-200">
            Both of these are read once at startup. Saving stores the new value, but promotion and the
            hardware probe keep using the old one until Codarr is restarted.
          </p>
          <FormField
            label="Temp directory"
            hint="Staging fallback for when the destination filesystem cannot hold the output alongside the source."
          >
            <TextInput value={form.temp_dir} onChange={(next) => set('temp_dir', next)} placeholder="/tmp" />
          </FormField>
          <FormField
            label="QSV device"
            hint="The render node the Quick Sync encoder opens. Changing it needs a hardware re-probe to mean anything."
          >
            <TextInput
              value={form.qsv_device}
              onChange={(next) => set('qsv_device', next)}
              placeholder="/dev/dri/renderD128"
            />
          </FormField>
        </div>
      </Panel>

      <Panel title="Scanning" icon="probe">
        <div className="space-y-5">
          <Toggle
            checked={form.scan_enabled}
            onChange={(next) => set('scan_enabled', next)}
            label="Scheduled scan"
            description="Walk every enabled root on a schedule. Webhooks work regardless."
          />
          <FormField label="Scan schedule" hint="Cron expression, server local time.">
            <TextInput value={form.scan_cron} onChange={(next) => set('scan_cron', next)} placeholder="0 4 * * *" />
          </FormField>
          <FormField
            label="Scan rate limit"
            hint="Files per second the walk is allowed to stat. Keeps a cold NFS walk from saturating the mount."
          >
            <TextInput
              type="number"
              value={String(form.scan_rate_limit_fps)}
              onChange={(next) => set('scan_rate_limit_fps', Number(next) || 1)}
            />
          </FormField>
        </div>
      </Panel>

      <Panel title="Queue behaviour" icon="list">
        <div className="space-y-5">
          <Toggle
            checked={form.prioritise_quick_jobs}
            onChange={(next) => set('prioritise_quick_jobs', next)}
            label="Prioritise quick jobs"
            description="Give remux and audio-only jobs a better default priority than full re-encodes."
          />
          <Toggle
            checked={form.full_hash_enabled}
            onChange={(next) => set('full_hash_enabled', next)}
            label="Whole-file hash at promotion"
            description="Records a full hash alongside the sparse fingerprint. Slower, and only useful for integrity checks."
          />
          <div className="flex items-center justify-between border-t border-slate-800 pt-4">
            <div>
              <p className="text-sm font-medium text-white">Queue is {paused ? 'paused' : 'running'}</p>
              <p className="text-xs text-slate-400">
                Pausing stops new jobs starting. A running job continues to completion.
              </p>
            </div>
            <Button
              variant="secondary"
              icon={paused ? 'play' : 'pause'}
              loading={pausing}
              onClick={toggleQueue}
            >
              {paused ? 'Resume' : 'Pause'}
            </Button>
          </div>
        </div>
      </Panel>
    </div>
  );
}
