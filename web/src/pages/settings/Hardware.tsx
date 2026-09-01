import { useEffect, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Icon } from '../../components/ui/Icon';
import { KeyValue } from '../../components/ui/StatTile';
import { LoadingSpinner } from '../../components/ui/LoadingSpinner';
import { Panel } from '../../components/ui/Panel';
import { toast } from '../../components/ui/Toast';
import { formatDateTime } from '../../lib/format';
import type { Hardware, HWCapability } from '../../api/types';

export default function SettingsHardware() {
  const [hardware, setHardware] = useState<Hardware | null>(null);
  const [loading, setLoading] = useState(true);
  const [probing, setProbing] = useState(false);

  useEffect(() => {
    unwrap(api.GET('/api/hardware'))
      .then(setHardware)
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, []);

  const probe = async () => {
    setProbing(true);
    try {
      setHardware(await unwrap(api.POST('/api/hardware/probe')));
      toast.success('Probe finished.');
    } catch {
      // Already toasted.
    } finally {
      setProbing(false);
    }
  };

  if (loading || !hardware) {
    return <LoadingSpinner message="Loading hardware probe..." />;
  }

  const failing = hardware.capabilities.filter((capability) => !capability.works);
  const softwareOnly = hardware.selected_encoder === 'libx265';

  // Severity follows the consequence, not the fact of a failure. plan.md 10.2 wants a software ENCODER
  // fallback shown loudly, because it turns a 20-minute job into a 4-hour one. A decode that falls back
  // costs some CPU on one source format, so rendering the two alike trains the operator to ignore both.
  const actionable = (capability: HWCapability) =>
    capability.direction === 'encode' ? softwareOnly : false;
  const needsAction = failing.some(actionable);

  return (
    <div className="space-y-6 p-8">
      <header className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-white">Hardware</h1>
          <p className="mt-1 text-sm text-slate-400">
            Compiled-in support is not working support. Only a real one-second encode decides which
            encoder gets used.
          </p>
        </div>
        <Button icon="refresh" loading={probing} onClick={probe}>
          Re-probe
        </Button>
      </header>

      {softwareOnly && (
        <div role="alert" className="flex items-start gap-3 rounded-xl border-2 border-red-500 bg-red-950 p-4">
          <Icon name="alert" size={22} className="mt-0.5 flex-shrink-0 text-red-400" />
          <div className="text-sm text-red-100">
            <p className="font-bold">Every job will run on the software encoder.</p>
            <p className="mt-1 text-xs text-red-200">
              No hardware encoder passed the probe, so x265 is doing the work. Expect jobs to take
              roughly an order of magnitude longer than they should.
            </p>
          </div>
        </div>
      )}

      <Panel title="Selection" icon="hardware">
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <KeyValue label="Selected encoder">
            <span className={softwareOnly ? 'font-semibold text-red-400' : 'text-green-400'}>
              {hardware.selected_encoder ?? 'none'}
            </span>
          </KeyValue>
          <KeyValue label="QSV device">{hardware.qsv_device || 'none'}</KeyValue>
          <KeyValue label="ffmpeg">{hardware.ffmpeg_version || 'unknown'}</KeyValue>
          <KeyValue label="Last probe">{formatDateTime(hardware.probed_at)}</KeyValue>
        </dl>
      </Panel>

      {hardware.remediation && failing.length > 0 && (
        <Panel title={needsAction ? 'Remediation' : 'Notes'} icon="shield">
          <p
            className={`text-sm whitespace-pre-wrap ${needsAction ? 'text-amber-200' : 'text-slate-400'}`}
          >
            {hardware.remediation}
          </p>
        </Panel>
      )}

      <Panel title={`Probe results (${hardware.capabilities.length})`} icon="probe">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-slate-800 text-xs tracking-wide text-slate-400 uppercase">
              <tr>
                <th className="py-2 pr-3 font-medium">Backend</th>
                <th className="py-2 pr-3 font-medium">Codec</th>
                <th className="py-2 pr-3 font-medium">Profile</th>
                <th className="py-2 pr-3 font-medium">Direction</th>
                <th className="py-2 pr-3 font-medium">Result</th>
                <th className="py-2 pr-3 font-medium">ffmpeg</th>
                <th className="py-2 font-medium">Probed</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800">
              {hardware.capabilities.map((capability) => (
                <tr key={`${capability.backend}-${capability.codec}-${capability.profile}-${capability.direction}`}>
                  <td className="py-2 pr-3 text-slate-200">{capability.backend}</td>
                  <td className="py-2 pr-3 text-slate-300">{capability.codec}</td>
                  <td className="py-2 pr-3 text-slate-300">{capability.profile}</td>
                  <td className="py-2 pr-3 text-slate-300">{capability.direction}</td>
                  <td className="py-2 pr-3">
                    {capability.works ? (
                      <Badge tone="success">works</Badge>
                    ) : (
                      <Badge tone={actionable(capability) ? 'danger' : 'neutral'} title={capability.error}>
                        {actionable(capability) ? 'failed' : 'unavailable'}
                      </Badge>
                    )}
                    {capability.error && (
                      <details className="mt-1">
                        <summary className="cursor-pointer text-xs text-slate-500 hover:text-slate-300">
                          driver message
                        </summary>
                        <p className="mt-1 max-w-prose text-xs break-words whitespace-pre-wrap text-slate-400">
                          {capability.error}
                        </p>
                      </details>
                    )}
                  </td>
                  <td className="py-2 pr-3 text-xs text-slate-500">{capability.ffmpeg_version ?? ''}</td>
                  <td className="py-2 text-xs text-slate-500">{formatDateTime(capability.probed_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel
        title="Hardware decode set"
        icon="zap"
        description="Hard-coded. Anything outside this list decodes in software, and -hwaccel is never passed for it."
      >
        <div className="flex flex-wrap gap-2">
          {hardware.hardware_decode_codecs.map((codec) => (
            <Badge key={codec} tone="info">
              {codec}
            </Badge>
          ))}
        </div>
      </Panel>
    </div>
  );
}
