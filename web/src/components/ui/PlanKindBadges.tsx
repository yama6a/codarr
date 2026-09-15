import { Badge } from './Badge';
import { humanise } from '../../lib/format';
import { planLabelTone } from '../../lib/tone';
import type { PlanKind } from '../../api/types';

/** PlanKindBadges renders one badge per label a plan carries, or "Skipped" when it carries none. */
export function PlanKindBadges({ kind }: { kind: PlanKind }) {
  if (kind.length === 0) {
    return <Badge tone="neutral">Skipped</Badge>;
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {kind.map((label) => (
        <Badge key={label} tone={planLabelTone(label)}>
          {humanise(label)}
        </Badge>
      ))}
    </span>
  );
}
