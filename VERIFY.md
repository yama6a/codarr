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

### The *arr naming formats carry no MediaInfo tokens (2026-09-01, resolved)

`plan.md` 23.2 requires renaming off **or** a naming format with no
`{MediaInfo ...}` tokens. The formats originally contained
`{Mediainfo VideoCodec}`, `{Mediainfo AudioCodec}`, `{Mediainfo AudioChannels}`
and `{MediaInfo VideoDynamicRangeType}`, so neither held.

The user rewrote all four to title, year and id only. Verified:

```
GET /api/v3/config/naming on each instance -> 0 MediaInfo tokens on all four

radarr-*  standardMovieFormat
  {Movie CleanTitle} ({Release Year}) {tmdb-{TmdbId}}{ edition-{Edition Tags}}
sonarr-*  standardEpisodeFormat
  {Series CleanTitleWithoutYear} ({Series Year}) - S{season:00}E{episode:00} - {Episode CleanTitle}
          seriesFolderFormat
  {Series CleanTitleWithoutYear} {(Series Year)} {tvdb-{TvdbId}}
```

23.2 is now satisfied on its second limb rather than waived, and renaming can
stay on. Nothing left in a filename derives from the file's contents, so the name
a rename pass would compute is invariant under everything Codarr does: a `full`
job changing the codec and an `audio_only` job changing the audio no longer move
the path. A future rename pass is a guaranteed no-op.

Consequence worth recording: `{Quality Full}` is gone too, so an *arr database
rebuilt from filenames alone would import everything as Unknown quality. The user
weighed that and does not want it.

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
| 1 | HDR10 metadata survives a `hevc_qsv` round trip | **CONFIRMED** |
| 2 | Plex `analyze` verb and path on the running PMS | **CONFIRMED**, `PUT`, 200 empty body |
| 3 | DOVI record survives a copy, and its ffprobe path | **CONFIRMED** for MKV; MP4 needs `dvh1` + `-strict unofficial` |
| 4 | Chrome client profile produces Direct Stream for 8-bit and 10-bit HEVC | TODO, needs a browser and a real file |
| 5 | *arr naming format uses no `{MediaInfo ...}` tokens | **CONFIRMED**, resolved by the user 2026-09-01 |
| 6 | Current jellyfin-ffmpeg Debian package name and repo path | **CONFIRMED** |
| 7 | `vainfo` inside the container, and which driver loaded | **CONFIRMED**, Intel iHD 25.4.6, jellyfin-bundled |
| 8 | 7.1 to 5.1 downmix produces a sensible channel layout | **CONFIRMED** |
| 9 | ASS to SRT conversion is readable on a real sample | **CONFIRMED**, with caveats |
| 10 | `rename()` atomicity and same device number on the NFS mount | **CONFIRMED** |
| 11 | Whether `chown` succeeds on the NFS mount | **FAILS**, expected and tolerated |
| 12 | CODARR tag survives a round trip into MP4 | **CONFIRMED**, MP4 output is not gated |
| 13 | VMAF spot-check, tuning the hardware correction | **DONE**, retuned 1.35 to 1.25 |
| 14 | VP9 hardware decode on this Gen 9.5 driver stack | **CONFIRMED**, stays in the hardware set |
| 15 | A level-rewritten file plays on the pickiest client | Bitstream rewrite **CONFIRMED**; playback still needs a television |
| 16 | Whether Bazarr preserves the CODARR global tag | Moot, Bazarr writes sidecars here |
| - | Upgrades disabled on all four instances (23.2) | **CONFIRMED** |
| - | *arr root folders all report `/media` | **CONFIRMED**, mappings mandatory |
| - | Plex path mapping not needed | **CONFIRMED** |
| - | QSV and VAAPI, HEVC Main and Main10 encode (10.1) | **CONFIRMED**, all four pass |

## Method for the pod-based items

A short-lived pod in the `media` namespace on `tc-w1`, requesting the free
`gpu.intel.com/i915` slot (Plex holds the other of two), running as uid 568 with
`media-library` mounted and an `emptyDir` for scratch. Sleep entrypoint,
`kubectl exec` per check, deleted afterwards. Nothing existing is touched.


---

## Verification pod run, 2026-09-01

One `codarr-verify` pod in `media` on `tc-w1`, holding the free `gpu.intel.com/i915`
slot, `jellyfin/jellyfin:latest` (the same jellyfin-ffmpeg7 7.1.4 our Dockerfile
installs), uid 568, library mounted, all test media synthesised with
`ffmpeg -f lavfi`. Pod deleted afterwards and confirmed gone; the namespace is
back to its original 12 pods and the i915 allocation back to Plex only. The one
scratch directory on the NAS, `/media/.codarr-verify-tmp/`, was removed.

### The hardware works, completely

Driver is the jellyfin-bundled **Intel iHD 25.4.6**, libva 2.23 / VA-API 1.23.
There is no distro driver in the image at all. `vainfo` reports HEVC Main and
Main10 EncSlice, and VLD for MPEG-2, H.264, VC-1, HEVC Main/Main10, VP8 and VP9
profiles 0 and 2. No AV1 VLD, exactly as 6.2 predicts for Gen 9.5.

All four 10.1 probes pass, `{qsv, vaapi}` x `{Main 8-bit, Main10 10-bit}`, and
re-running them to real files rather than `-f null` confirms the pixel formats.
**Main10 does not fail**, so 10.1's "the cause is almost certainly the driver
stack" caveat does not apply here.

VP9 hardware decode works on both profile 0 and profile 2, including a full
GPU-to-GPU `vp9_qsv` to `hevc_qsv` chain. `vp9` stays in the hardware-decode set.

### The level rewrite works end to end

`h264_metadata=level=4.2` rewrote 51 to 42 in both MKV and MP4, the SPS is
changed in the raw bitstream, and `framemd5` is identical before and after. So
6.2's central claim holds: the flag changes and not one decoded pixel does.

### HDR10 survives, and more thoroughly than asked

Mastering display and MaxCLL/MaxFALL both survive a `hevc_qsv` round trip, in
the raw HEVC elementary stream rather than only in the container. Verified by
stripping to a bare `.265` and reprobing.

### The CODARR tag survives into MP4, and `use_metadata_tags` is load-bearing

All three keys read back from `-show_format`. MP4 output is not gated.

Worth knowing: **without `-movflags use_metadata_tags` all three vanish and
ffmpeg still exits 0.** A silent success that produces an untagged output is
exactly the loop section 12 exists to prevent, so that flag is not optional
polish.

### NFS behaves as 15.6 assumes

- A real `rename(2)` over an existing file in the same directory succeeds and
  swaps the inode. No EXDEV. The positive control, renaming from the container's
  emptyDir into `/media`, correctly returns EXDEV(18), so the test discriminates.
  **This confirms staging must stay on the NFS mount**; if 15.1's staging ever
  moves to local scratch, promotion silently stops being atomic.
- Device number `1048591` is identical across `/media`, the scratch dotdir, the
  dotfile and its target. The 15.4 device comparison is meaningful.
- `chown` fails EPERM for any uid or gid change; `chmod` and `touch -d` both
  succeed. 15.6 already requires tolerating this. Note that EPERM here is **not**
  evidence of `root_squash`: uid 568 is unprivileged, so POSIX forbids it either
  way. Moot, since the library is uniformly 568:568 and Codarr writes as 568.
- Mount options in effect:
  `nfs4 rw,noatime,vers=4.2,rsize=1048576,wsize=1048576,softerr,softreval,fatal_neterrors=none,proto=tcp,timeo=600,retrans=5,sec=sys`

### 7.1 to 5.1 downmix is correct

`5.1(side)` for AC-3 and E-AC-3, `5.1` for FLAC; the label difference is the
codec's, not ffmpeg's. A per-channel tone energy check shows BL and SL folding
into Ls at -4.65 dB and -7.66 dB. Nothing dropped, nothing misrouted.

### ASS to SRT is readable, with defects worth knowing

The default `subrip` encoder leaks `{\an8}` positioning as visible text, leaks
`\h` hard spaces, and turns `{\p1}` vector drawings into garbage lines.
`-c:s text -f srt` is clean but drops italics. 6.4 already accepts losing
styling; this says the default encoder loses it *messily* rather than cleanly.

### The full image builds and runs, 2026-09-01

`docker buildx build --platform linux/amd64 -f .build/Dockerfile` succeeds. The
resulting 441 MB image starts, applies all five migrations, serves `/healthz`,
the SPA, `/api/policy` (hash `914f0f87`), `/api/dashboard` and 27 Prometheus
series, running as uid 568 with `ffmpeg 7.1.4-Jellyfin` on the PATH.

With no GPU passed through, the hardware probe correctly failed over and logged
`software_fallback: true` with `encoder: libx265`, which is 10.2's loud
reporting doing its job.

## Dolby Vision, settled with a real file

The earlier synthetic test was wrong, and it was wrong in the way it suspected.

A genuine profile 5 file was built with `dovi_tool 2.3.3`: `generate` a profile 5
RPU (240 frames, L5 and L6), `inject-rpu` into a 10-bit HEVC elementary stream,
muxed with `mkvmerge 99.0`. `dovi_tool info` confirms 240 real RPUs, profile 5,
CM v2.9.

**The configuration record DOES survive `-c:v copy` into MKV.** ffprobe reads it
back intact (`dv_profile: 5`, `dv_level: 3`, `rpu_present_flag: 1`), including on
a realistic `audio_only` job with the video copied and the audio re-encoded to
AC-3. The earlier finding was an artifact of a hand-injected record with no RPUs
behind it: the muxers were refusing a record the bitstream did not back up.

**`plan.md` 9's profile 5 gate is satisfiable and needs no change.**

The ffprobe path, confirmed against a real file:

```
.streams[].side_data_list[]
  | select(.side_data_type == "DOVI configuration record")
  | .dv_profile
```

**The in-band RPUs always survive**, 240 of 240 frames, in every container, with
or without the container record. The distinction section 9 draws between the two
is exactly right.

### MP4 exposed a real bug in plan.md 14.1, now fixed

The mov muxer writes the record only when the sample entry tag is `dvh1` or
`dvhe` **and** `-strict unofficial` is set. With `hvc1` it writes nothing **and
warns at no log level**.

`plan.md` 14.1 mandates `-tag:v hvc1` for HEVC in MP4. On a Dolby Vision source
that flag silently destroys the configuration record, which 15.3 then correctly
fails as a hard error for profile 5. Verified directly: DV MP4 in, `-tag:v hvc1`
out, record gone; the same command without the tag override keeps it.

Fixed in `internal/ffmpeg`: a Dolby Vision plan targeting MP4 emits
`-strict unofficial -tag:v dvh1` instead. Everything else still gets `hvc1`, as
14.1 requires. Covered by a golden file.

One thing that cannot be done at all: an MP4 tagged `dvh1` will not remux into
MKV (`Tag dvh1 incompatible with output codec id '173'`, header write fails), and
neither `-tag:v 0` nor `-tag:v ""` clears it. Container preservation (6.1) means
Codarr never attempts that.

## The hardware correction should be 1.25, not 1.35

`plan.md` 8.1 proposes 1.35 and says to tune it after this check.

jellyfin-ffmpeg 7.1.4 has no `libvmaf` (only `vmafmotion`), so a static build was
used for scoring; every encode ran on the jellyfin build against the real UHD
630. For each clip: the bitrate at which `hevc_qsv` reaches the same VMAF as the
probe's own `libx265 -crf 21 -preset veryfast` on identical frames.

| clip | reference kbps | reference VMAF | QSV kbps at equal VMAF | ratio |
|---|---|---|---|---|
| Tears of Steel, live action A | 1368 | 81.73 | 1604 | 1.17 |
| Sintel t=204 | 3674 | 97.19 | 4395 | 1.20 |
| Xiph in_to_tree | 18304 | 90.86 | 22677 | 1.24 |
| Tears of Steel, live action B | 1125 | 73.09 | 1453 | 1.29 |
| Sintel t=444 | 2706 | 75.06 | 3610 | 1.33 |
| Sintel t=684 | 2761 | 87.11 | 3683 | 1.33 |

Mean and median both **1.26**. Changed to **1.25**. 1.35 was not wrong, just
about 8% conservative.

The constant is only meaningful paired with the probe's `-preset veryfast`;
change one and the other has to be re-measured. Source material was Xiph's test
corpus and the Blender open movies, all freely licensed.

Side finding: `hevc_qsv` overshoots high targets (+6.6% at 18 Mbps, +8.7% at
46 Mbps) but lands within 1.5% below 5 Mbps, which is where this library sits.

## Plex analyze, confirmed against a real item

PMS 1.43.3.10896. One synthetic file was placed in `Movies-Yama`, scanned,
tested, and removed; the library is back to 0 items.

- **`PUT /library/metadata/{ratingKey}/analyze` returns 200 with an empty body.**
  GET and POST both 404, so the verb is required rather than preferred. A bad key
  404s, no token 401s.
- **It is non-destructive and sufficient on its own.** The file was replaced in
  place with a completely different encode and `analyze` called alone: duration,
  width, video codec and audio codec all updated within 15 seconds, while
  `title`, `addedAt`, `viewCount` and `userRating` were untouched. That is
  exactly Codarr's case, since the path does not change.

Two caveats worth folding into 16.1:

- **A partial refresh does not re-read a changed file at a known path.** Four
  polls over 48 seconds after an in-place swap still showed stale duration and
  codec; only `analyze` updated it. The refresh does pick up a *new* file in a
  known directory within 12 seconds, and does nothing at all for a directory Plex
  has never seen. Since Codarr replaces in place and never creates directories,
  **`analyze` is the load-bearing call and the refresh is the optional one**,
  which is the reverse of how 16.1 reads.
- **`file=` is a case-insensitive substring match**, not an exact one. A bogus
  path correctly returns nothing and an unknown filter is silently ignored, so
  the filter does work, but the client's re-check of every returned `Part.file`
  is load-bearing. Keep it.

## Smaller findings## Smaller findings

- **The native `eac3` encoder refuses 7.1** and downmixes to `5.1(side)` without
  being asked. Codarr never encodes to E-AC-3 (6.3's targets are AAC and AC-3),
  so nothing depends on this, but any future branch assuming E-AC-3 carries eight
  channels on this build would be wrong.
- **6.2's `refs <= 4` guard reads the SPS `max_num_ref_frames`, which encoders
  clamp downward.** `x264 -refs 3` produced `refs=1`. The guard is still correct;
  it just reads lower than the nominal encoder setting, which makes it more
  conservative rather than less.
- **The mount is `softerr`, not `hard`.** A write can fail mid-promotion with EIO
  after roughly five minutes of retries if the NAS hiccups. 15.6 discusses
  `softerr` as a node-wedging tradeoff but says nothing about that failure
  landing mid-job. Verification catches a truncated output, so it fails safe.
