import { useEffect, useState } from 'react';
import { api, unwrap } from '../../api/client';
import { Badge } from '../../components/ui/Badge';
import { Icon } from '../../components/ui/Icon';
import { KeyValue } from '../../components/ui/StatTile';
import { LoadingSpinner } from '../../components/ui/LoadingSpinner';
import { Panel } from '../../components/ui/Panel';
import { formatBytes } from '../../lib/format';
import type { Policy } from '../../api/types';

function Chips({ values }: { values: string[] }) {
  if (values.length === 0) {
    return <span className="text-xs text-slate-500">none</span>;
  }
  return (
    <span className="flex flex-wrap gap-1">
      {values.map((value) => (
        <Badge key={value} tone="neutral">
          {value}
        </Badge>
      ))}
    </span>
  );
}

function yesNo(value: boolean): string {
  return value ? 'yes' : 'no';
}

function channelRange(min: number, max: number): string {
  return max === 0 ? `${min}+ channels` : `${min} to ${max} channels`;
}

export default function SettingsPolicy() {
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    unwrap(api.GET('/api/policy'))
      .then(setPolicy)
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, []);

  if (loading || !policy) {
    return <LoadingSpinner message="Loading policy..." />;
  }

  return (
    <div className="space-y-6 p-8">
      <header>
        <h1 className="text-2xl font-bold text-white">Policy</h1>
        <p className="mt-1 text-sm text-slate-400">
          What gets transcoded, and to what.
        </p>
      </header>

      <div className="flex items-start gap-3 rounded-xl border border-sky-800 bg-sky-950/50 p-4">
        <Icon name="gavel" size={22} className="mt-0.5 flex-shrink-0 text-sky-400" />
        <div className="text-sm text-sky-100">
          <p className="font-semibold">This is compiled into the binary. There is nothing to edit.</p>
          <p className="mt-1 text-xs text-sky-200">
            No setting anywhere in Codarr changes any value on this page. Changing the policy means
            changing Go constants and rebuilding, which changes the policy hash below and makes every
            already-tagged file eligible for a re-check again.
          </p>
          <p className="mt-2 font-mono text-xs text-sky-300">policy_hash {policy.policy_hash}</p>
        </div>
      </div>

      <Panel title="Container" icon="file">
        <dl className="space-y-3">
          <KeyValue label="Preserved extensions">
            <Chips values={policy.container.preserve_extensions} />
          </KeyValue>
          <KeyValue label="Always rewritten to Matroska">
            <Chips values={policy.container.legacy_extensions} />
          </KeyValue>
          <KeyValue label="MP4 movflags">
            <Chips values={policy.container.mp4_movflags} />
          </KeyValue>
        </dl>
      </Panel>

      <Panel title="Video" icon="video">
        <div className="space-y-5">
          <dl className="grid gap-4 sm:grid-cols-2">
            <KeyValue label="Copy codecs">
              <Chips values={policy.video.copy_rule.codecs} />
            </KeyValue>
            <KeyValue label="Chroma subsampling">{policy.video.copy_rule.chroma_subsampling}</KeyValue>
            <KeyValue label="H.264 profiles copied">
              <Chips values={policy.video.copy_rule.h264_profiles} />
            </KeyValue>
            <KeyValue label="HEVC profiles copied">
              <Chips values={policy.video.copy_rule.hevc_profiles} />
            </KeyValue>
            <KeyValue label="H.264 maximum level">{policy.video.copy_rule.h264_max_level}</KeyValue>
            <KeyValue label="Progressive only">{yesNo(policy.video.copy_rule.progressive_only)}</KeyValue>
            <KeyValue label="Unknown scan counts as progressive">
              {yesNo(policy.video.copy_rule.unknown_scan_is_progressive)}
            </KeyValue>
            <KeyValue label="Drop attached pictures">{yesNo(policy.video.drop_attached_pictures)}</KeyValue>
          </dl>

          <div className="border-t border-slate-800 pt-4">
            <p className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Level rewrite</p>
            <p className="mb-2 text-xs text-slate-400">
              A stream that fails only the level test is still copied. The flag is rewritten in-stream
              during the container rebuild.
            </p>
            <dl className="grid gap-4 sm:grid-cols-3">
              <KeyValue label="Target level">{policy.video.level_rewrite.target_level}</KeyValue>
              <KeyValue label="Maximum size">
                {policy.video.level_rewrite.max_width}x{policy.video.level_rewrite.max_height}
              </KeyValue>
              <KeyValue label="Maximum fps">{policy.video.level_rewrite.max_fps}</KeyValue>
              <KeyValue label="Maximum refs">{policy.video.level_rewrite.max_refs}</KeyValue>
              <KeyValue label="Bitstream filter" className="sm:col-span-2">
                <code className="text-xs">{policy.video.level_rewrite.bitstream_filter}</code>
              </KeyValue>
            </dl>
          </div>

          <div className="border-t border-slate-800 pt-4">
            <p className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Encode targets</p>
            <ul className="space-y-1 text-sm text-slate-300">
              {policy.video.encode_targets.map((target) => (
                <li key={`${target.source}-${target.codec}-${target.profile}`}>
                  {target.source} becomes {target.codec} {target.profile}, {target.bit_depth}-bit
                  {target.mp4_tag ? `, MP4 tag ${target.mp4_tag}` : ''}
                </li>
              ))}
            </ul>
          </div>

          <div className="border-t border-slate-800 pt-4">
            <p className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">
              Hardware decode set
            </p>
            <Chips values={policy.video.hardware_decode_codecs} />
          </div>
        </div>
      </Panel>

      <Panel title="Audio" icon="audio">
        <div className="space-y-5">
          <div>
            <p className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Copy list</p>
            <ul className="space-y-1 text-sm text-slate-300">
              {policy.audio.copy_list.map((rule, index) => (
                <li key={index}>
                  {channelRange(rule.min_channels, rule.max_channels)}: {rule.codecs.join(', ')}
                </li>
              ))}
            </ul>
          </div>
          <div className="border-t border-slate-800 pt-4">
            <p className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Encode targets</p>
            <ul className="space-y-1 text-sm text-slate-300">
              {policy.audio.encode_targets.map((target, index) => (
                <li key={index}>
                  {channelRange(target.min_channels, target.max_channels)}: {target.codec} at{' '}
                  {target.bitrate_kbps} kbps
                  {target.downmix_channels ? `, downmixed to ${target.downmix_channels} channels` : ''}
                </li>
              ))}
            </ul>
          </div>
          <dl className="grid gap-4 border-t border-slate-800 pt-4 sm:grid-cols-2">
            <KeyValue label="MP4 multichannel codec">{policy.audio.mp4_multichannel_codec}</KeyValue>
            <KeyValue label="MP4 kbps per channel">{policy.audio.mp4_kbps_per_channel}</KeyValue>
            <KeyValue label="Keep all languages">{yesNo(policy.audio.keep_all_languages)}</KeyValue>
            <KeyValue label="Never zero audio streams">{yesNo(policy.audio.never_zero_audio_streams)}</KeyValue>
          </dl>
        </div>
      </Panel>

      <Panel title="Subtitles" icon="subtitle">
        <dl className="space-y-3">
          <KeyValue label="Dropped image codecs">
            <Chips values={policy.subtitles.drop_image_codecs} />
          </KeyValue>
          <KeyValue label="Dropped broadcast codecs">
            <Chips values={policy.subtitles.drop_broadcast_codecs} />
          </KeyValue>
          <KeyValue label="Text codecs">
            <Chips values={policy.subtitles.text_codecs} />
          </KeyValue>
          <KeyValue label="Targets">
            {policy.subtitles.targets.map((target) => `${target.container} to ${target.codec}`).join(', ')}
          </KeyValue>
          <KeyValue label="Drop attachments">{yesNo(policy.subtitles.drop_attachments)}</KeyValue>
          <KeyValue label="Keep all languages">{yesNo(policy.subtitles.keep_all_languages)}</KeyValue>
          <KeyValue label="Drop forced image subtitles">
            {yesNo(policy.subtitles.drop_forced_image_subtitles)}
          </KeyValue>
        </dl>
      </Panel>

      <Panel title="Bitrate" icon="gauge" description={policy.bitrate.applies_to}>
        <div className="space-y-5">
          <dl className="grid gap-4 sm:grid-cols-4">
            <KeyValue label="HDR uplift">{policy.bitrate.hdr_uplift_pct}%</KeyValue>
            <KeyValue label="fps scale cap">{policy.bitrate.fps_scale_cap}</KeyValue>
            <KeyValue label="maxrate factor">{policy.bitrate.maxrate_factor}</KeyValue>
            <KeyValue label="bufsize factor">{policy.bitrate.bufsize_factor}</KeyValue>
          </dl>

          <div className="border-t border-slate-800 pt-4">
            <p className="mb-2 text-xs font-semibold tracking-wide text-slate-400 uppercase">Sample probe</p>
            <dl className="grid gap-4 sm:grid-cols-4">
              <KeyValue label="Segments">
                {policy.bitrate.sample_probe.segments} x {policy.bitrate.sample_probe.segment_seconds}s
              </KeyValue>
              <KeyValue label="Skip head/tail">
                {policy.bitrate.sample_probe.skip_head_pct}% / {policy.bitrate.sample_probe.skip_tail_pct}%
              </KeyValue>
              <KeyValue label="Encoder">
                {policy.bitrate.sample_probe.encoder}, CRF {policy.bitrate.sample_probe.crf},{' '}
                {policy.bitrate.sample_probe.preset}
              </KeyValue>
              <KeyValue label="Hardware correction">{policy.bitrate.sample_probe.hardware_correction}</KeyValue>
              <KeyValue label="Source clamp">{policy.bitrate.sample_probe.source_clamp}</KeyValue>
            </dl>
          </div>

          <div className="border-t border-slate-800 pt-4">
            <table className="w-full text-left text-sm">
              <thead className="border-b border-slate-800 text-xs tracking-wide text-slate-400 uppercase">
                <tr>
                  <th className="py-2 pr-3 font-medium">Resolution</th>
                  <th className="py-2 pr-3 font-medium">Bits per pixel</th>
                  <th className="py-2 pr-3 font-medium">Floor</th>
                  <th className="py-2 font-medium">Ceiling</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-800 text-slate-300">
                {policy.bitrate.table.map((row) => (
                  <tr key={row.resolution}>
                    <td className="py-2 pr-3">{row.resolution}</td>
                    <td className="py-2 pr-3">{row.bpp}</td>
                    <td className="py-2 pr-3">{row.floor_kbps} kbps</td>
                    <td className="py-2">{row.ceiling_kbps} kbps</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </Panel>

      <Panel title="Space reclaim sweep" icon="disk">
        <dl className="grid gap-4 sm:grid-cols-4">
          <KeyValue label="Manual only">{yesNo(policy.space_sweep.manual_only)}</KeyValue>
          <KeyValue label="Source codec">{policy.space_sweep.source_codec}</KeyValue>
          <KeyValue label="Minimum video bitrate">{policy.space_sweep.min_video_bitrate_kbps} kbps</KeyValue>
          <KeyValue label="Minimum projected saving">{policy.space_sweep.min_projected_saving_pct}%</KeyValue>
        </dl>
      </Panel>

      <Panel title="Exclusions" icon="ban">
        <dl className="space-y-3">
          <KeyValue label="Extras directories">
            <Chips values={policy.exclusions.extras_directories} />
          </KeyValue>
          <KeyValue label="Filename patterns">
            <Chips values={policy.exclusions.filename_patterns} />
          </KeyValue>
          <KeyValue label="Partial extensions">
            <Chips values={policy.exclusions.partial_extensions} />
          </KeyValue>
          <KeyValue label="Minimum size">{formatBytes(policy.exclusions.min_size_bytes)}</KeyValue>
          <KeyValue label="Stability guard">{policy.exclusions.stability_guard_seconds} seconds</KeyValue>
        </dl>
      </Panel>

      <Panel
        title="Loop prevention tags"
        icon="fingerprint"
        description="Written into every output. The tag alone never skips a file: the rule is a conjunction with the policy hash and the recorded output fingerprint."
      >
        <Chips values={policy.tag_keys} />
      </Panel>
    </div>
  );
}
