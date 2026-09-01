import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { Icon } from '../ui/Icon';
import { KeyValue } from '../ui/StatTile';
import { formatBytes, formatDateTime, provenanceLabel } from '../../lib/format';
import { provenanceTone } from '../../lib/tone';
import type { IntegrityResult, MediaDetail } from '../../api/types';

interface ProvenanceSectionProps {
  media: MediaDetail;
  integrity: IntegrityResult | null;
  verifying: boolean;
  onVerify: () => void;
}

// plan.md 17.1 and 18.3: the recorded output fingerprint against the current one, and a plain
// statement when they differ.
export function ProvenanceSection({ media, integrity, verifying, onVerify }: ProvenanceSectionProps) {
  const modified = media.provenance === 'modified_since_transcode';
  const written = Boolean(media.codarr_output_fingerprint);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={provenanceTone(media.provenance)}>{provenanceLabel(media.provenance)}</Badge>
        {media.codarr_tagged && <Badge tone="info">CODARR tag present</Badge>}
        <Button variant="secondary" icon="fingerprint" loading={verifying} onClick={onVerify}>
          Verify integrity now
        </Button>
      </div>

      {modified && (
        <div role="alert" className="flex gap-3 rounded-lg border-2 border-red-500 bg-red-950 p-4">
          <Icon name="shield" size={22} className="mt-0.5 flex-shrink-0 text-red-400" />
          <div className="text-sm text-red-100">
            <p className="font-bold">Something rewrote this file after Codarr produced it.</p>
            <p className="mt-1 text-xs text-red-200">
              The fingerprint on disk no longer matches the one recorded at promotion. A Bazarr
              subtitle embed or a manual import is the usual cause. Codarr will re-plan this file on
              the next check rather than trusting its tag.
            </p>
          </div>
        </div>
      )}

      {!written && !modified && (
        <p className="text-xs text-slate-400">Codarr has never written this file, so there is nothing to compare.</p>
      )}

      <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <KeyValue label="Recorded fingerprint">
          <span className="font-mono text-xs break-all">{media.codarr_output_fingerprint || 'none'}</span>
        </KeyValue>
        <KeyValue label="Current fingerprint">
          <span className={`font-mono text-xs break-all ${modified ? 'text-red-300' : ''}`}>
            {media.fingerprint || 'unknown'}
          </span>
        </KeyValue>
        <KeyValue label="Fingerprint algorithm">{media.fingerprint_algo || 'unknown'}</KeyValue>
        <KeyValue label="Recorded full hash">
          <span className="font-mono text-xs break-all">{media.codarr_output_full_hash || 'not recorded'}</span>
        </KeyValue>
        <KeyValue label="Produced by job">
          {media.codarr_job_id ? `#${media.codarr_job_id}` : 'none'}
        </KeyValue>
        <KeyValue label="Produced at">{formatDateTime(media.codarr_processed_at)}</KeyValue>
        <KeyValue label="Recorded size">
          {media.codarr_output_size ? formatBytes(media.codarr_output_size) : 'none'}
        </KeyValue>
        <KeyValue label="Current size">{formatBytes(media.size_bytes)}</KeyValue>
        <KeyValue label="Policy hash at promotion">
          <span className="font-mono text-xs break-all">{media.codarr_policy_hash || 'none'}</span>
        </KeyValue>
        <KeyValue label="Last integrity check">{formatDateTime(media.integrity_checked_at)}</KeyValue>
      </dl>

      {integrity && (
        <div
          className={`rounded-lg border p-3 text-xs ${
            integrity.ok ? 'border-green-800 bg-green-950/50 text-green-200' : 'border-red-800 bg-red-950/50 text-red-200'
          }`}
        >
          <p className="font-semibold">
            {integrity.ok ? 'Still byte-identical to what Codarr wrote.' : 'The file no longer matches.'}
          </p>
          {integrity.message && <p className="mt-1">{integrity.message}</p>}
          <p className="mt-1 text-slate-400">
            Checked {formatDateTime(integrity.checked_at)}
            {integrity.full_hash_checked ? ', whole-file hash included' : ', sparse fingerprint only'}
          </p>
        </div>
      )}
    </div>
  );
}
