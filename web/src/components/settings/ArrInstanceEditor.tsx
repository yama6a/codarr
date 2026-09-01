import { useState } from 'react';
import { Button } from '../ui/Button';
import { FormField } from '../ui/FormField';
import { Modal } from '../ui/Modal';
import { Select } from '../ui/Select';
import { TextInput } from '../ui/TextInput';
import { Toggle } from '../ui/Toggle';
import { PathMappings } from './PathMappings';
import { SecretInput } from './SecretInput';
import type { ArrInstance, ArrInstanceUpdate, Flavour } from '../../api/types';

export interface ArrEditorValue extends ArrInstanceUpdate {
  id?: number;
}

interface ArrInstanceEditorProps {
  instance: ArrInstance | null;
  saving: boolean;
  onSave: (value: ArrEditorValue) => void;
  onCancel: () => void;
}

const flavours = [
  { value: 'radarr', label: 'Radarr' },
  { value: 'sonarr', label: 'Sonarr' },
];

function initial(instance: ArrInstance | null): ArrEditorValue {
  if (!instance) {
    return {
      name: '',
      flavour: 'radarr',
      base_url: '',
      api_key: '',
      rescan_after: true,
      unmonitor_after: false,
      enabled: true,
      path_mappings: [],
    };
  }
  return {
    id: instance.id,
    name: instance.name,
    flavour: instance.flavour,
    base_url: instance.base_url,
    api_key: instance.api_key,
    rescan_after: instance.rescan_after,
    unmonitor_after: instance.unmonitor_after,
    enabled: instance.enabled,
    path_mappings: instance.path_mappings,
  };
}

export function ArrInstanceEditor({ instance, saving, onSave, onCancel }: ArrInstanceEditorProps) {
  const [form, setForm] = useState<ArrEditorValue>(() => initial(instance));

  const set = <K extends keyof ArrEditorValue>(key: K, value: ArrEditorValue[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }));

  const creating = instance === null;
  const valid = form.name.trim() !== '' && form.base_url.trim() !== '' && form.api_key.trim() !== '';

  return (
    <Modal
      open
      onClose={onCancel}
      title={creating ? 'Add instance' : `Edit ${instance.name}`}
      footer={
        <>
          <Button variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button icon="save" loading={saving} disabled={!valid} onClick={() => onSave(form)}>
            Save
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <FormField label="Name" required>
          <TextInput value={form.name} onChange={(next) => set('name', next)} placeholder="radarr-4k" />
        </FormField>

        <FormField label="Flavour" required>
          <Select
            ariaLabel="Flavour"
            value={form.flavour}
            options={flavours}
            onChange={(next) => set('flavour', next as Flavour)}
            className="w-full"
          />
        </FormField>

        <FormField label="Base URL" required>
          <TextInput
            value={form.base_url}
            onChange={(next) => set('base_url', next)}
            placeholder="http://radarr:7878"
          />
        </FormField>

        <FormField
          label="API key"
          required
          hint={
            creating
              ? 'Settings, General, Security in the instance itself.'
              : 'Stored keys are never returned. Leave it untouched to keep the current one.'
          }
        >
          {creating ? (
            <TextInput
              type="password"
              value={form.api_key}
              onChange={(next) => set('api_key', next)}
              ariaLabel="API key"
            />
          ) : (
            <SecretInput label="API key" value={form.api_key} onChange={(next) => set('api_key', next)} />
          )}
        </FormField>

        <Toggle
          checked={form.enabled}
          onChange={(next) => set('enabled', next)}
          label="Enabled"
          description="A disabled instance is not notified and cannot own a root."
        />
        <Toggle
          checked={form.rescan_after}
          onChange={(next) => set('rescan_after', next)}
          label="Rescan after a replacement"
          description="RescanMovie or RescanSeries, so the instance picks up the new file size and codec."
        />
        <Toggle
          checked={form.unmonitor_after}
          onChange={(next) => set('unmonitor_after', next)}
          label="Unmonitor after a full job"
          description="Stops the instance grabbing an upgrade that would undo the transcode. Off by default."
        />

        <div className="border-t border-slate-800 pt-4">
          <p className="mb-2 text-sm font-medium text-white">Path mappings</p>
          <PathMappings
            value={form.path_mappings}
            remoteLabel={`${form.flavour} path`}
            onChange={(next) => set('path_mappings', next)}
          />
        </div>
      </div>
    </Modal>
  );
}
