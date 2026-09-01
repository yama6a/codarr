import { Badge } from '../ui/Badge';
import { Icon } from '../ui/Icon';
import { formatBitrate, humanise } from '../../lib/format';
import { decisionTone, planKindTone } from '../../lib/tone';
import type { Plan, StreamPlan } from '../../api/types';

function flags(stream: StreamPlan): string[] {
  const out: string[] = [];
  if (stream.default) out.push('default');
  if (stream.forced) out.push('forced');
  if (stream.comment) out.push('comment');
  if (stream.visual_impaired) out.push('visual impaired');
  return out;
}

function target(stream: StreamPlan): string {
  const parts: string[] = [];
  if (stream.target_codec) parts.push(stream.target_codec);
  if (stream.target_channels) parts.push(`${stream.target_channels} ch`);
  if (stream.target_bitrate_bps) parts.push(formatBitrate(Math.round(stream.target_bitrate_bps / 1000)));
  return parts.length > 0 ? parts.join(', ') : 'unchanged';
}

/**
 * PlanSection renders the decision engine's output for a file that has never had a job. The after
 * side is always the plan here, and the label says so.
 */
export function PlanSection({ plan }: { plan: Plan }) {
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 rounded-lg border border-sky-800 bg-sky-950/50 px-3 py-2 text-xs text-sky-200">
        <Icon name="clock" size={16} />
        No job has run for this file. Everything below is the planned target, not a produced result.
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={planKindTone(plan.kind)}>{humanise(plan.kind)}</Badge>
        <Badge tone="neutral">
          {plan.source_container} to {plan.output_container}
        </Badge>
        {plan.level_rewrite && <Badge tone="info">H.264 level rewrite</Badge>}
        {plan.deinterlace && <Badge tone="warning">Deinterlace</Badge>}
        {plan.hdr && <Badge tone="warning">HDR</Badge>}
        {plan.dolby_vision && <Badge tone="warning">Dolby Vision profile {plan.dolby_vision_profile ?? '?'}</Badge>}
      </div>

      {plan.reasons.length > 0 && (
        <ul className="list-inside list-disc space-y-1 text-xs text-slate-400">
          {plan.reasons.map((reason) => (
            <li key={reason}>{reason}</li>
          ))}
        </ul>
      )}

      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead className="text-slate-500">
            <tr className="border-b border-slate-800">
              <th className="py-2 pr-3 font-medium">Type</th>
              <th className="py-2 pr-3 font-medium">Source</th>
              <th className="py-2 pr-3 font-medium">Output</th>
              <th className="py-2 pr-3 font-medium">Action</th>
              <th className="py-2 pr-3 font-medium">Target</th>
              <th className="py-2 pr-3 font-medium">Language</th>
              <th className="py-2 font-medium">Reason</th>
            </tr>
          </thead>
          <tbody className="text-slate-300">
            {plan.streams.map((stream) => (
              <tr key={`${stream.type}-${stream.source_index}`} className="border-b border-slate-800/60">
                <td className="py-2 pr-3">{stream.type}</td>
                <td className="py-2 pr-3">{stream.source_index}</td>
                <td className="py-2 pr-3">{stream.output_index ?? 'none'}</td>
                <td className="py-2 pr-3">
                  <Badge tone={decisionTone(stream.decision)}>{humanise(stream.decision)}</Badge>
                </td>
                <td className="py-2 pr-3">{target(stream)}</td>
                <td className="py-2 pr-3">
                  {stream.language || 'und'}
                  {flags(stream).length > 0 && (
                    <span className="ml-1 text-slate-500">({flags(stream).join(', ')})</span>
                  )}
                </td>
                <td className="py-2 text-slate-400">{stream.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
