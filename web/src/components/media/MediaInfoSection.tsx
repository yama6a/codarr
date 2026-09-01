import { formatBitrate, formatDuration, formatResolution } from '../../lib/format';
import { KeyValue } from '../ui/StatTile';
import type { MediaInfo, MediaInfoAudio, MediaInfoSubtitle } from '../../api/types';

function audioFlags(track: MediaInfoAudio): string {
  const out: string[] = [];
  if (track.default) out.push('default');
  if (track.forced) out.push('forced');
  if (track.comment) out.push('comment');
  if (track.visual_impaired) out.push('visual impaired');
  return out.join(', ') || 'none';
}

function subtitleFlags(track: MediaInfoSubtitle): string {
  const out: string[] = [];
  if (track.default) out.push('default');
  if (track.forced) out.push('forced');
  return out.join(', ') || 'none';
}

/** MediaInfoSection is the deliberately partial view plan.md 18.3 asks for; probe_json holds the dump. */
export function MediaInfoSection({ info }: { info: MediaInfo }) {
  return (
    <div className="space-y-4">
      <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <KeyValue label="Container">{info.container}</KeyValue>
        <KeyValue label="Duration">{formatDuration(info.duration_seconds)}</KeyValue>
        <KeyValue label="Attachments">{info.attachments}</KeyValue>
        <KeyValue label="Chapters">{info.chapters}</KeyValue>
      </dl>

      {info.video && (
        <div>
          <h4 className="mb-1 text-xs font-semibold tracking-wide text-slate-400 uppercase">Video</h4>
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <KeyValue label="Codec">{info.video.codec}</KeyValue>
            <KeyValue label="Profile">
              {info.video.profile || 'unknown'}
              {info.video.level ? ` L${info.video.level}` : ''}
            </KeyValue>
            <KeyValue label="Resolution">{formatResolution(info.video.width, info.video.height)}</KeyValue>
            <KeyValue label="Frame rate">{info.video.fps ? `${info.video.fps.toFixed(3)} fps` : 'unknown'}</KeyValue>
            <KeyValue label="Bitrate">{formatBitrate(info.video.bitrate_kbps)}</KeyValue>
            <KeyValue label="Pixel format">{info.video.pix_fmt || 'unknown'}</KeyValue>
            <KeyValue label="Scan">{info.video.scan}</KeyValue>
            <KeyValue label="HDR">{info.video.hdr ? 'yes' : 'no'}</KeyValue>
          </dl>
        </div>
      )}

      <div>
        <h4 className="mb-1 text-xs font-semibold tracking-wide text-slate-400 uppercase">
          Audio tracks ({info.audio.length})
        </h4>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-slate-500">
              <tr className="border-b border-slate-800">
                <th className="py-1.5 pr-3 font-medium">#</th>
                <th className="py-1.5 pr-3 font-medium">Codec</th>
                <th className="py-1.5 pr-3 font-medium">Channels</th>
                <th className="py-1.5 pr-3 font-medium">Layout</th>
                <th className="py-1.5 pr-3 font-medium">Bitrate</th>
                <th className="py-1.5 pr-3 font-medium">Language</th>
                <th className="py-1.5 pr-3 font-medium">Title</th>
                <th className="py-1.5 font-medium">Disposition</th>
              </tr>
            </thead>
            <tbody className="text-slate-300">
              {info.audio.map((track) => (
                <tr key={track.index} className="border-b border-slate-800/60">
                  <td className="py-1.5 pr-3">{track.index}</td>
                  <td className="py-1.5 pr-3">
                    {track.codec}
                    {track.profile ? ` (${track.profile})` : ''}
                  </td>
                  <td className="py-1.5 pr-3">{track.channels}</td>
                  <td className="py-1.5 pr-3">{track.layout}</td>
                  <td className="py-1.5 pr-3">{formatBitrate(track.bitrate_kbps)}</td>
                  <td className="py-1.5 pr-3">{track.language}</td>
                  <td className="py-1.5 pr-3">{track.title ?? ''}</td>
                  <td className="py-1.5 text-slate-400">{audioFlags(track)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div>
        <h4 className="mb-1 text-xs font-semibold tracking-wide text-slate-400 uppercase">
          Subtitle tracks ({info.subtitles.length})
        </h4>
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="text-slate-500">
              <tr className="border-b border-slate-800">
                <th className="py-1.5 pr-3 font-medium">#</th>
                <th className="py-1.5 pr-3 font-medium">Codec</th>
                <th className="py-1.5 pr-3 font-medium">Language</th>
                <th className="py-1.5 pr-3 font-medium">Title</th>
                <th className="py-1.5 font-medium">Disposition</th>
              </tr>
            </thead>
            <tbody className="text-slate-300">
              {info.subtitles.map((track) => (
                <tr key={track.index} className="border-b border-slate-800/60">
                  <td className="py-1.5 pr-3">{track.index}</td>
                  <td className="py-1.5 pr-3">{track.codec}</td>
                  <td className="py-1.5 pr-3">{track.language}</td>
                  <td className="py-1.5 pr-3">{track.title ?? ''}</td>
                  <td className="py-1.5 text-slate-400">{subtitleFlags(track)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
