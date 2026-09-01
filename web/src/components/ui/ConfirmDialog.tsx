import React from 'react';
import { Button } from './Button';
import { Icon } from './Icon';
import { Modal } from './Modal';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  confirmLabel: string;
  busy?: boolean;
  confirmDisabled?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  children: React.ReactNode;
}

// plan.md 15.5: promotion destroys the original, so every bulk action needs a preview and an
// explicit confirmation.
export function ConfirmDialog({
  open,
  title,
  confirmLabel,
  busy = false,
  confirmDisabled = false,
  onConfirm,
  onCancel,
  children,
}: ConfirmDialogProps) {
  return (
    <Modal
      open={open}
      onClose={onCancel}
      title={title}
      size="md"
      footer={
        <>
          <Button variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="danger" loading={busy} disabled={confirmDisabled} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {children}
        <IrreversibleNotice />
      </div>
    </Modal>
  );
}

// plan.md 15.5 wants this reasoning visible in the UI, not only in the README.
export function IrreversibleNotice() {
  return (
    <div className="flex gap-3 rounded-lg border border-red-800 bg-red-950/50 p-4">
      <Icon name="alert" size={20} className="mt-0.5 flex-shrink-0 text-red-400" />
      <div className="space-y-2 text-xs text-red-200">
        <p className="text-sm font-semibold text-red-100">This cannot be undone.</p>
        <p>
          Promotion replaces the original file with a single atomic rename. There is no trash, no
          retention window and no restore path.
        </p>
        <p>
          If a transcode ruins a file, Radarr or Sonarr re-fetching it is the only recovery
          mechanism.
        </p>
      </div>
    </div>
  );
}
