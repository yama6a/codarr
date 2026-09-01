# Verification against live systems

`plan.md` section 27 lists claims that were not confirmed against a running
system when it was written. Each is recorded here with the date, the command
that produced the answer, and the answer.

Anything still `TODO` is an assumption the code currently rests on.

Cluster: `admin@yama-cluster`. Plex at `192.168.100.21:32400`. Library on
`192.168.10.220:/mnt/pool/media`.

## Findings that change the work

### The library is empty (2026-09-01)

```
kubectl -n media exec deploy/plex -- find /media -type f \( -name '*.mkv' -o -name '*.mp4' \) | wc -l
0
```

All four Plex sections report 0 items. Nothing to analyze, nothing to transcode,
nothing to regress. Two consequences:

- The verification pod has to synthesise its own test media with
  `ffmpeg -f lavfi`. That covers the QSV probes, HDR10 round trip, the CODARR
  MP4 tag, 7.1 to 5.1 downmix, ASS to SRT and VP9 decode.
- Dolby Vision cannot be verified at all: an RPU cannot be synthesised. Item 3
  stays open until a real DV file exists.

### All four *arr instances have renaming ON, with MediaInfo tokens (2026-09-01)

This contradicts `plan.md` 23.2, which requires renaming off **or** a naming
format with no `{MediaInfo ...}` tokens. Neither holds.

```
GET /api/v3/config/naming on each instance
radarr-yama, radarr-kostas:  renameMovies=true
sonarr-yama, sonarr-kostas:  renameEpisodes=true
```

Every format contains `{Mediainfo VideoCodec}`, `{Mediainfo AudioCodec}`,
`{Mediainfo AudioChannels}` and `{MediaInfo VideoDynamicRangeType}`.

Why it matters: Codarr never renames, and keeps the path stable so a rescan is a
no-op rather than a delete-plus-add. But a `full` job changes the codec, and a
DTS to AC3 conversion changes the audio tokens. The next *arr rename pass then
churns the path anyway, which is exactly what `plan.md` 16.2 wants to avoid, and
Plex sees one file removed and another added.

**This is not something Codarr can fix from its side.** Either renaming goes off
on all four instances, or the `{MediaInfo ...}` tokens come out of the four
formats, before the write path is pointed at the real library.

### Upgrades are off on every instance (2026-09-01, confirms 23.2)

```
GET /api/v3/qualityprofile on each instance
every profile on all four instances: upgradeAllowed = false
```

The premise `plan.md` 16.2 rests on holds: a rescan cannot trigger an upgrade
search. `unmonitor_after` stays unnecessary and ships off by default.

### Every *arr reports the same root folder, `/media` (2026-09-01)

```
GET /api/v3/rootfolder on each instance -> ["/media"] on all four
```

Each *arr mounts only its own slice via `subPath`, so `/media` inside
`radarr-yama` IS `/media/yama/movies` on the NAS. Codarr mounts the export whole
and sees the real paths.

**Per-instance path mappings are therefore mandatory, not optional**, and root
import must apply the instance's mapping before creating the root row.
Otherwise all four instances import the literal path `/media`, the
longest-prefix attribution in `plan.md` 16.2 has four identical candidates, and
the same-root collision check fires on every one of them.

Required mappings:

| Instance | remote | local |
|---|---|---|
| radarr-yama | `/media` | `/media/yama/movies` |
| sonarr-yama | `/media` | `/media/yama/tv` |
| radarr-kostas | `/media` | `/media/kostas/movies` |
| sonarr-kostas | `/media` | `/media/kostas/tv` |

### Plex needs no path mapping (2026-09-01)

```
GET /library/sections
3 movie 'Movies-Kostas' ['/media/kostas/movies']
1 movie 'Movies-Yama'   ['/media/yama/movies']
4 show  'TV-Kostas'     ['/media/kostas/tv']
2 show  'TV-Yama'       ['/media/yama/tv']
```

Plex mounts the export whole, exactly as Codarr will, so its view of a path and
Codarr's are the same string. `plex_path_mappings` stays empty, and the
reverse-mapping step before comparing a session's file path is a no-op here.
Keep the mechanism anyway; it costs nothing and the assumption is one mount
change away from breaking.

### jellyfin-ffmpeg7 is the right package, and it is new enough (2026-09-01)

```
curl -fsSL https://repo.jellyfin.org/debian/dists/bookworm/main/binary-amd64/Packages.gz \
  | gunzip | awk '/^Package: jellyfin-ffmpeg7$/{p=1} p&&/^Version:/{print; p=0}'
Version: 7.1.4-3-bookworm
```

The package name in `plan.md` 22 is current, and `deb https://repo.jellyfin.org/debian bookworm main`
resolves. Version 7.1.4 matters for one thing beyond currency: `plan.md` 9 notes
that Matroska Dolby Vision support landed in ffmpeg 6.1, so a DOVI configuration
record can survive `-c:v copy` into MKV on this build. That is a necessary
condition for the profile 5 gate, not a sufficient one; it still needs a real
file to prove.

### The runtime image builds, and carries every codec the plan needs (2026-09-01)

Built the runtime stage standalone for linux/amd64 under emulation.

```
ffmpeg version 7.1.4-Jellyfin / ffprobe version 7.1.4-Jellyfin
hwaccels: cuda vaapi qsv drm opencl vulkan
encoders: hevc_qsv, hevc_vaapi, libx265
decoders: libdav1d (AV1 software decode), h264_qsv, hevc_qsv
bsfs:     h264_metadata
uid=568 gid=568
```

So the four things the encode path depends on are all compiled in: `hevc_qsv`
and its `hevc_vaapi` fallback (10.2), `libx265` for the software fallback and
the 8.1 sample probe, `libdav1d` for the AV1 software-decode path (6.2), and the
`h264_metadata` bitstream filter for the level rewrite (6.2).

Compiled-in is not working, per 10.1. Whether QSV actually encodes Main and
Main10 on this silicon still needs the verification pod.

### Bazarr does not depend on *arr video analysis (2026-09-01)

Relevant to whether "Analyse video files" can be turned off on the *arrs.

```
embedded_subtitles_parser: ffprobe      # Bazarr runs its OWN ffprobe
parse_embedded_audio_track: false       # audio language comes from the *arr
language_equals: []                     # nothing branches on audio language
default_und_audio_lang: ''
single_language / use_original_language / force_audio: false
ignore_pgs_subs: false
```

Embedded subtitle detection is Bazarr's own ffprobe, independent of the *arr.
Audio language is the only field it takes from the *arr, and no setting acts on
it. Setting `parse_embedded_audio_track: true` removes even that, at no extra
I/O, since Bazarr already reads those files.

`use_embedded_subs: false` on BOTH instances is the master switch, and it makes
`ignore_pgs_subs`, `ignore_vobsub_subs` and `ignore_ass_subs` inert: they only
apply when embedded subtitles are being counted. So Bazarr never treats an
embedded track as satisfying a language, and already fetches an external
subtitle for every wanted language whatever is inside the file. Codarr dropping
PGS tracks (6.4) therefore changes nothing about Bazarr's behaviour.

The `embeddedsubtitles` provider is separately enabled with
`included_codecs: []`, meaning all codecs. Worth pinning to text formats so it
never tries to extract a PGS or VobSub track, which cannot become a useful
sidecar without OCR.

### Bazarr writes sidecars, so section 12's central scenario does not apply here (2026-09-01)

```
subfolder: current      # sidecar .srt beside the video, on both instances
```

`plan.md` 12 builds the skip rule around one specific case: Bazarr embedding a
subtitle track via mkvmerge, which carries global tags through, leaving a file
that still wears a valid CODARR tag and a matching policy hash but now contains
a PGS track Codarr would have stripped. Trusting the tag alone would make that
file invisible forever.

As configured, Bazarr never rewrites a video file. A sidecar changes the
directory, not the file, so it does not move the fingerprint.

The tag-plus-fingerprint conjunction stays implemented regardless: it costs
nothing, and it is one settings change away from mattering. But
`modified_since_transcode` should be rare here rather than routine.

Bazarr also runs its own per-instance path mapping (`/media/` to `/media/tv/`),
which is the same remapping Codarr needs and independently confirms the root
folder finding above.

## Section 27 checklist

| # | Claim | Status |
|---|---|---|
| 1 | HDR10 metadata survives a `hevc_qsv` round trip | TODO, verification pod |
| 2 | Plex `analyze` verb and path on the running PMS | TODO, blocked: no items in any library |
| 3 | ffprobe JSON path for the Dolby Vision profile, and DOVI record survival on copy | TODO, blocked: no DV source exists and an RPU cannot be synthesised |
| 4 | Chrome client profile produces Direct Stream for 8-bit and 10-bit HEVC | TODO, needs a browser and a real file |
| 5 | *arr renaming is off on all four instances | **FAILED**, see above. Renaming is on and the formats use MediaInfo tokens |
| 6 | Current jellyfin-ffmpeg Debian package name and repo path | **CONFIRMED**, see below |
| 7 | `vainfo` inside the container, and which driver loaded | PARTIAL: vainfo present and the image builds; which driver loads needs the pod |
| 8 | 7.1 to 5.1 downmix produces a sensible channel layout | TODO, verification pod |
| 9 | ASS to SRT conversion is readable on a real sample | TODO, verification pod |
| 10 | `rename()` atomicity and same device number on the NFS mount | TODO, verification pod |
| 11 | Whether `chown` succeeds on the NFS mount | TODO, verification pod |
| 12 | CODARR tag survives a round trip into MP4 | TODO, verification pod |
| 13 | VMAF spot-check, tuning the 1.35 hardware correction | TODO, needs real content |
| 14 | VP9 hardware decode on this Gen 9.5 driver stack | TODO, verification pod |
| 15 | A level-rewritten file plays on the pickiest client | TODO, needs a television |
| 16 | Whether Bazarr preserves the CODARR global tag | TODO, needs a real subtitle run |
| - | Upgrades disabled on all four instances (23.2) | **CONFIRMED** |
| - | *arr root folders all report `/media` | **CONFIRMED**, mappings mandatory |
| - | Plex path mapping not needed | **CONFIRMED** |

## Method for the pod-based items

A short-lived pod in the `media` namespace on `tc-w1`, requesting the free
`gpu.intel.com/i915` slot (Plex holds the other of two), running as uid 568 with
`media-library` mounted and an `emptyDir` for scratch. Sleep entrypoint,
`kubectl exec` per check, deleted afterwards. Nothing existing is touched.
