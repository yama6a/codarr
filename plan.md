# Codarr - Build Specification

Build a single-user media transcoding service in Go. It watches media produced
by Radarr and Sonarr, decides whether each file will direct-play on Plex without
server-side transcoding, and rewrites only what needs rewriting.

This is not a general-purpose tool. There is one owner, one Plex server, one
machine. The encoding policy is hard-coded in Go constants and is not
configurable. Deployment wiring (instances, paths, credentials) lives in the
database and is edited through the UI.

Where this document says VERIFY, the claim was not confirmed against a live
system. Check it before depending on it.

Reasoning is included where it should end up in a code comment or the README -
places where the obvious implementation is wrong, or where a future reader would
otherwise "simplify" a deliberate choice back into a bug. Everything else is
stated as a requirement without justification.

---

## 1. Goals, in priority order

1. **Direct play.** Nothing in the library should force Plex to transcode video
   at playback time.
2. **Disk space.** Real, but subordinate. Where the two conflict, compatibility
   wins.

Space reclamation is a separate manual sweep (section 11), never automatic.

---

## 2. Development approach

This is not optional scaffolding. Build it this way from commit one.

### 2.1 OpenAPI first

`api/openapi.yaml` is the source of truth for the HTTP surface. Write the spec
before the handlers. Generate, never hand-write, the server plumbing.

Use `oapi-codegen` targeting **Chi**:

```go
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen \
//   -config oapi-codegen.yaml ../../api/openapi.yaml
```

```yaml
# oapi-codegen.yaml
package: api
output: internal/api/generated.go
generate:
  chi-server: true
  models: true
  strict-server: true      # typed request/response, no manual marshalling
  embedded-spec: true      # serve the spec at /api/openapi.json
output-options:
  skip-prune: true
```

`strict-server` is worth it: handlers become
`func (s *Server) GetJob(ctx, req GetJobRequestObject) (GetJobResponseObject, error)`
which is trivially unit-testable without `httptest` in most cases.

Implement the generated `StrictServerInterface` by hand in `internal/api`.
Regenerating must never overwrite handler logic.

**Generate the frontend client from the same spec.** Use `openapi-typescript`
plus `openapi-fetch` in the Vite build. This keeps request and response types in
sync automatically and is the main payoff of doing spec-first.

**There is no streaming in this API.** The UI polls (18.6), so every endpoint is
a plain request/response and the whole surface generates cleanly.

**The *arr webhook payloads are the one thing to model loosely.** They are
external schemas that vary by event type and *arr version. Declare only the
fields actually read, with `additionalProperties: true`. Mirroring them exactly
creates breakage on every *arr release.

### 2.2 Mocks with moq

Define narrow interfaces at every boundary and generate mocks with
`github.com/matryer/moq`.

```go
//go:generate go run github.com/matryer/moq -out probe_moq_test.go . Prober
```

| Interface | Why |
| --- | --- |
| `Prober` | wraps ffprobe - lets the decision engine be tested with JSON fixtures |
| `Encoder` | wraps ffmpeg execution and progress |
| `PlexClient` | sessions, libraries, refresh, analyze |
| `ArrClient` | one per flavour, rescan and root folder discovery |
| `Store` | all DB access |
| `Clock` | deterministic time in tests |
| `FS` | stat, rename, space checks, so promotion logic is testable |

Keep interfaces small and defined by the consumer, not the implementation.
`Prober` should have one method, not fifteen.

### 2.3 TDD, unit and integration

Write the test first. Every phase in section 25 is complete only when its tests
pass.

**Unit tests** cover the pure functions, where the real complexity lives and
where bugs are silent:

- **Decision engine.** Table-driven, fed synthetic ffprobe JSON. The single most
  important test suite in the project. Cover every branch of the copy tests;
  every codec on and off the copy lists; the level-rewrite carve-out including
  the `refs > 4` rejection; the exact profile strings including "Constrained
  Baseline"; 10-bit 4:2:0 chroma acceptance; unknown and absent `field_order`;
  HDR; Dolby Vision profile 5; AV1 and the other software-decode sources;
  interlaced; attached picture streams; zero-audio edge cases; and the
  tag-plus-fingerprint skip conjunction from section 12, including the case where
  the CODARR tag and policy hash match but the fingerprint does not.
- **ffmpeg argv builder.** Golden files. Given a plan, assert the exact argv.
  This catches the output-stream-index trap in 14.2, which is otherwise
  invisible, and pins decode-path selection, the level-rewrite bitstream filter,
  `hvc1` tagging and the MP4 movflags.
- **Bitrate calculator.** Floors, ceilings, clamps, the hardware correction, and
  the source-bitrate fallback chain in 8.4.
- **Path mapper.** Prefix rewriting both directions, longest-prefix instance
  attribution, overlapping roots.
- **Policy hash.** Stable across runs, changes when constants change.
- **Transform record** construction and merge-on-completion.

**Integration tests:**

- Store against a real temporary SQLite file: migrations, WAL behaviour, queue
  claim/lease under concurrent access, the duplicate-enqueue no-op, and the
  interrupted-requeue startup sweep with its attempt cap.
- API handlers against the generated router with moq'd services.
- Plex and *arr clients against `httptest` servers replaying recorded fixtures.
  Capture real responses once, commit the JSON, replay forever.
- ffmpeg and ffprobe against tiny fixtures generated at test time with
  `ffmpeg -f lavfi`. Skip with a build tag when ffmpeg is absent.

**A caveat on fixtures:** ffmpeg cannot readily *produce* DTS or TrueHD, so those
paths cannot be integration-tested end to end. Test them at the decision layer
with synthetic ffprobe JSON. This gap is a reason to test the decision engine
harder, not a reason to test less.

**Not tested:** actual hardware encoding. It cannot run in CI. Cover the
capability probe's parsing and fallback logic with mocks; verify real encoding by
hand.

Target: decision engine, argv builder and bitrate calculator at or near 100%
branch coverage. Everything else, best effort.

---

## 3. Hard rules

1. One Go binary. Frontend embedded via `go:embed`. Vite + React + TypeScript,
   Node at build time only.
2. Backend under `/api`. SPA fallback to `index.html` for everything else.
3. **Encoding policy is hard-coded.** No setting changes what gets transcoded or
   to what. Policy lives in Go constants and is displayed read-only in the UI.
4. **All configuration lives in SQLite** and is edited through the UI. Only
   bootstrap values come from flags or environment (section 21).
5. **Exactly one transcode at a time.** A single goroutine consuming a queue.
6. **Always replace in place.** No output directories, no filename suffixes.
7. **Never replace a file Plex is currently streaming.** Defer, do not skip.
8. **Resolution is never changed.** No scaling filters.
9. **Audio channel count is never changed** except 7.1 and above downmixed to
   5.1 when a conversion is required anyway.

---

## 4. Terminology

x264 and x265 are **encoders**. H.264 (AVC) and H.265 (HEVC) are **codecs**. This
box produces HEVC via `hevc_qsv`, a different encoder from x265 and meaningfully
less efficient at the same bitrate. Never write "x265" when meaning the codec.

**Bit depth is not an independent dimension.** It is expressed through profile:
10-bit H.264 *is* High 10; 10-bit HEVC *is* Main 10. Chroma subsampling beyond
4:2:0 also lives in its own profiles. This is why a codec-name-only check misses
High 10.

**Level encodes resolution and frame rate**, so resolution is not a separate
compatibility check. H.264 L4.1 tops out near 1080p30 and L4.2 covers 1080p60.
3840x2160 does not fit below **L5.1** - level 5.0's maximum frame size is 22080
macroblocks and 4K is 32400 - and many H.264 hardware decoders never implemented
5.1. HEVC has no equivalent problem at 4K because 4K hardware decoders were built
around it.

**HDR requires 10-bit; 10-bit does not require HDR.** HDR is metadata (BT.2020
primaries, PQ transfer, static mastering display data) alongside a 10-bit stream.
Strip the metadata and you have valid video that renders washed out.

---

## 5. Clients and playback modes

Target clients: LG WebOS Plex app, Plex Android app, CHiQ TV (Android TV based),
and a web browser.

| Mode | What happens | Cost |
| --- | --- | --- |
| Direct Play | File sent as-is | none |
| Direct Stream | Container remuxed, streams copied | trivial |
| Transcode | Video re-encoded | expensive |

All four clients play HEVC. The three TV clients do so natively; the browser does
so through Plex Web, which added HEVC support for Chromium browsers in 4.94.2 -
**but only once the server-side client profile in 23.1 is installed.** Without
that profile Plex Media Server transcodes HEVC to the browser regardless of what
the browser can decode, because PMS decides from its own client profile rather
than from browser capability.

A browser cannot take MKV, so it never Direct Plays from an MKV library. With the
profile installed it Direct Streams instead: the container is remuxed and the
video stream copied, which is trivial.

**Valid H.264 is copied, never upgraded to HEVC.** Not for browser reasons -
re-encoding already-compatible video is pure generation loss for no gain.

LG WebOS does not support PGS subtitles, which drives the image-subtitle policy.

---

## 6. Policy (hard-coded)

### 6.1 Container: preserve it, so the filename never changes

**MKV stays MKV. MP4 (and M4V) stays MP4. Everything else becomes MKV.**

The path stays stable, which matters for two reasons beyond tidiness:

- The *arrs track files by path. An unchanged path means a rescan is a no-op
  rather than a delete-plus-add.
- Plex matches media parts by path. A changed path makes the scanner see one file
  removed and another added. Watched state lives on the item rather than the
  part so it usually survives, but there is no reason to take the risk.

**MP4 can hold everything this policy produces.** H.264 and HEVC are both fully
supported (`avc1` and `hvc1`; see the tag rule in 6.2). AAC is native. Subtitles
follow the general policy.

**Legacy containers always become MKV**, even when every stream copies: AVI, WMV,
MPEG-TS, VOB, FLV, MPG, M2TS, MOV, OGM. Poor or absent seek indexes, weak support
for multiple audio tracks and subtitles, inconsistent client handling. These
files are almost always being fully re-encoded anyway, so an extension change is
the smallest thing happening to them.

**When the output container is MP4, two rules differ:**

| | MKV output | MP4 output |
| --- | --- | --- |
| Subtitle target | `srt` (subrip) | `mov_text` |
| Audio conversion target, 3+ channels | AC3 640k | **AAC** at 64 kbps per channel |

Subtitles: `mov_text` (tx3g) is the only text format written into MP4. Do not
assume anything about what an MP4 input contains - VobSub-in-MP4 exists in the
wild (MP4Box, Nero) and WebVTT rides in fragmented MP4. No special handling is
needed because the general policy already covers every case: image formats drop
regardless of container, text formats convert to the container's target. The rule
exists so the argv builder emits the right codec, not as an invariant about
inputs.

Audio: AC3 inside MP4 is legal per spec but support is patchy, since players
expect AC3 in MKV or TS. Multichannel AAC in MP4 is universally supported. This
branch is rare - MP4 files in the wild are overwhelmingly H.264 plus AAC, both of
which copy.

**MP4 outputs always get `-movflags +faststart+use_metadata_tags`.**
`faststart` moves the moov atom to the front so playback and seeking start
instantly over HTTP; it costs a second pass in which ffmpeg rewrites the finished
file, roughly one extra read plus write, and only MP4 outputs pay it.
`use_metadata_tags` carries the loop-prevention tag from section 12.

### 6.2 Video: copy first

```
COPY the video stream if ALL of:
  codec    in {h264, hevc}
  profile  in {Constrained Baseline, Baseline, Main, High}  when codec == h264
           in {Main, Main 10}                               when codec == hevc
  level    <= 4.2, OR the level rewrite below applies       when codec == h264
  chroma   is 4:2:0 subsampling, at any bit depth
  scan     is progressive; unknown or absent counts as progressive
ENCODE otherwise.
```

Copy regardless of bitrate. A high-bitrate file that already direct-plays is a
space concern, handled by the sweep in section 11.

Three of these tests have non-obvious forms, and getting any of them wrong
re-encodes valid files:

- **Profiles match ffprobe's exact strings, and "Constrained Baseline" is one of
  them.** It is a distinct string from "Baseline". Unknown or missing profile
  means encode (default deny), but the phase-1 dry run surfaces plan counts, so a
  probe quirk mass-classifying files is visible before anything is written.
- **Chroma is a subsampling test, not a `pix_fmt` string compare.** `yuv420p`,
  `yuv420p10le` and `p010le` all pass. A string compare against `yuv420p` makes
  every HEVC Main 10 file fail its own copy test. Bit-depth legality is already
  policed by the profile test: 10-bit H.264 reports High 10 and fails there.
- **`field_order` unknown or absent counts as progressive.** ffprobe omits it, or
  reports `unknown`, for a large share of progressive files; treating that as
  "not progressive" full-encodes much of the library. Only explicit interlaced
  values (`tt`, `bb`, `tb`, `bt`) force deinterlacing.

  Residual risk: genuinely interlaced material reporting unknown. For h264 and
  hevc that combination is rare and copy is the right bet. For encode-path legacy
  codecs (mpeg2video, vc1) with unknown `field_order`, run a short `idet` sample -
  decode roughly 500 frames, read the counters - and deinterlace if it reports
  interlaced. Those are the sources where baked-in combing actually happens.

**Level rewrite instead of re-encode.** Many 1080p H.264 files are stamped level
5.0 or 5.1 by encoder defaults. The flag is what strict decoders reject; the
content fits 4.2 fine. When a stream fails ONLY the level test and all of:

- `width <= 1920` and `height <= 1088`
- `fps <= 60`
- `refs <= 4`

then rewrite the level in-stream during the container rebuild:

```
-bsf:v h264_metadata=level=4.2
```

The stream is still copied - no decode, no quality change - and the plan kind
stays `remux` or `audio_only`. Content genuinely above 4.2 goes to encode.

The `refs <= 4` guard is load-bearing, not a safety margin: level 4.2 allows
MaxDpbMbs 34816, a 1080p frame is 8160 macroblocks, so the decoded picture buffer
holds 4 reference frames. An encode using more legitimately needs the higher level
and must not be down-flagged. Golden-file coverage for this branch is mandatory,
including the `refs > 4` rejection.

**What reaches the encode path:** MPEG-2 (DVD rips), VC-1 (old Blu-rays), VP9,
AV1, WMV, MPEG-4 ASP / Xvid, H.264 High 10, H.264 above L4.2 where the level
rewrite does not apply, interlaced content of any codec, and anything not 4:2:0.
A small minority of a normal library.

**Attached picture streams.** MKV and MP4 files frequently carry cover art as a
video stream with `disposition.attached_pic == 1`. **Always drop these, and never
mistake one for the primary video stream.** Selecting the real video stream means
finding the first video stream where `attached_pic` is 0, not blindly taking
`0:v:0`.

**Encode target:**

| Source | Target |
| --- | --- |
| SDR, any resolution | HEVC Main, 8-bit |
| HDR, any resolution | HEVC Main 10 |

One codec family, all resolutions. The copy rule above is untouched: valid H.264
is never upgraded.

**When the output container is MP4, tag HEVC as `hvc1`** (`-tag:v hvc1`).
ffmpeg's default is `hev1`, which Apple-derived players and some TVs refuse. MKV
has no tag distinction.

**AV1 sources are re-encoded to HEVC.** UHD 630 has no AV1 hardware decoder -
that arrived with Gen 12 - so AV1 software-decodes via dav1d, which is
multithreaded and comfortably faster than realtime even at 4K, then
hardware-encodes like everything else. Expect wall clock at or better than 1x
playback speed.

This is deliberately a downgrade in coding efficiency: AV1 is more efficient than
HEVC, so the output will usually be larger than the input. It is accepted because
AV1 sources are rare here and guaranteed direct play outranks size. Do not
"optimise" AV1 onto the copy list.

VP9 does have hardware decode on Gen 9.5; the probe in 10.1 confirms it.

### 6.3 Audio: copy list

**Copy the track if its codec is on this list for its channel count:**

| Channels | Copy these |
| --- | --- |
| 1-2 | aac, ac3, eac3, mp3 |
| 3+ | aac, ac3, eac3 |

**Otherwise encode to:**

| Channels | Codec | Bitrate |
| --- | --- | --- |
| 1 | AAC-LC | 96 kbps |
| 2 | AAC-LC | 160 kbps |
| 3-6 | AC3 | 640 kbps |
| 7+ | AC3, downmixed to 5.1 (`-ac 6`) | 640 kbps |

Reasoning worth keeping:

- **AAC, AC3, EAC3, MP3 are copied** because all four target clients decode them.
  Re-encoding a compatible track is generation loss for no compatibility gain.
  EAC3 is what most streaming WEB-DL releases ship; converting it would degrade a
  large slice of the library to fix a problem these clients do not have.
- **DTS and DTS-HD are converted** because LG's support is unreliable by model
  year: removed from WebOS in 2020, restored in 2023, removed again in 2025. A
  compatibility policy cannot rest on that.
- **TrueHD, FLAC, PCM are converted** because they are lossless, enormous and
  poorly supported by TV clients. Atmos rides inside TrueHD or EAC3-JOC;
  re-encoding TrueHD strips the object metadata. Accepted and irreversible.
- **Opus, Vorbis, ALAC and anything else are converted.** Default deny.
- **AC3 rather than EAC3 as the conversion target** because AC3 is universally
  decodable and fits every transport including optical S/PDIF and basic HDMI ARC,
  neither of which can carry EAC3. AC3 caps at 5.1 and 640 kbps, the Blu-ray
  Dolby Digital standard and well past audible.
- **AAC rather than AC3 for stereo and mono** because AC3 is inefficient at low
  channel counts and browsers decode AAC but not AC3.

Use ffmpeg's native `aac` and `ac3` encoders. Do not reach for `libfdk_aac`; it
is not GPL-compatible for redistribution and will not be in the image.

Keep every audio track and every language. Preserve per-track `language`, `title`
and disposition flags (`default`, `comment`, `visual_impaired`); ffmpeg does not
always carry dispositions through, so set them explicitly.

**Never produce a file with zero audio streams.** If the mapping logic would do
that, fail the job.

The 7.1 downmix rarely fires: a 7.1 EAC3 or AAC track is on the copy list and
passes through untouched. It only triggers for TrueHD 7.1 or DTS-HD MA 7.1.

### 6.4 Subtitles

**Drop all image-based subtitles**: `hdmv_pgs_subtitle` (PGS), `dvd_subtitle`
(VobSub), `dvb_subtitle`.

These are bitmaps. When a client cannot render them the server burns them into
the video, forcing a full playback transcode. LG WebOS cannot render PGS.

**Forced image subtitles drop with the rest.** Keeping forced PGS would
reintroduce exactly the burn-in transcode this policy exists to remove. Forced
*text* subtitles are kept with their `forced` disposition, and every dropped
forced track is recorded in the transform record, so the rare file worth
revisiting is findable.

**Drop broadcast-embedded caption formats**: `dvb_teletext`, `eia_608`.
Teletext conversion needs a libzvbi-enabled decoder plus page selection, EIA-608
usually lives inside the video stream rather than as a real track, and both appear
almost exclusively in broadcast captures that are being fully re-encoded anyway.
Large machinery, unreliable output, low-value track. Record the reason.

**Every standalone text subtitle is kept**, converted to whatever the output
container supports. Standalone text means `subrip`, `ass`, `ssa`, `webvtt`,
`mov_text`; a subtitle in one of these is never dropped for format reasons.

| Output container | Text subtitle target |
| --- | --- |
| MKV | `srt` (subrip) |
| MP4 | `mov_text` (tx3g) |

Copy when the source codec already equals the target for that container, convert
otherwise. Sources that convert: `ass`, `ssa`, `webvtt`, plus `subrip` into an
MP4 and `mov_text` into an MKV.

ASS and SSA carry styling, positioning and karaoke effects many clients cannot
render, so Plex burns them in for those clients - the same failure mode as PGS.
Converting to SRT loses that styling, which matters for anime typesetting and
essentially nothing else. It also removes the dependency on embedded font
attachments, so **drop all attachments**.

**Keep every language.** No language filtering.

Preserve `default` and `forced` dispositions and language tags. A file may
legitimately end up with zero subtitle streams.

---

## 7. The plan

Per file, an independent decision per stream.

```go
type Decision string
const (
    Copy    Decision = "copy"
    Encode  Decision = "encode"
    Convert Decision = "convert"  // subtitles only
    Drop    Decision = "drop"
)

type Kind string
const (
    KindSkip      Kind = "skip"
    KindRemux     Kind = "remux"
    KindAudioOnly Kind = "audio_only"
    KindFull      Kind = "full"
)
```

| Kind | Condition | Expected frequency | Cost |
| --- | --- | --- | --- |
| `skip` | all streams copy, container is MKV or MP4 | common | none |
| `remux` | all streams copy, container is legacy -> MKV | occasional | I/O only |
| `audio_only` | video copies, audio or subtitle work needed | **dominant** | I/O bound |
| `full` | video needs re-encoding | rare | CPU/GPU bound |

With copy-first video, most of the library is `audio_only`: fix the DTS track,
drop the PGS, copy the video. The cost is reading and writing the whole file
because the container is rebuilt; the audio encoding itself is negligible, since
all streams within one ffmpeg run encode concurrently.

**Do not collapse `audio_only` into `full`.**

The level rewrite from 6.2 is a copy with a bitstream filter attached: the
decision stays `copy`, the reason records the rewrite, the plan kind is unchanged.

Every decision records a human-readable reason, stored and shown in the UI:

```
video: COPY - h264 High L4.0 8-bit 4:2:0 progressive
video: COPY - level 5.1 -> 4.2 flag rewrite (content fits 4.2, refs=3)
video 1: DROP - attached picture (cover art)
audio 0 (eng, 5.1): ENCODE - dts not in copy list for 3+ channels
audio 1 (ger, 2.0): COPY - aac, stereo
subtitle 0 (eng, subrip): COPY
subtitle 1 (eng, ass): CONVERT - ass to srt
subtitle 2 (eng, hdmv_pgs_subtitle): DROP - image-based
container: matroska -> matroska
plan: AUDIO_ONLY - video copied, 1 audio stream re-encoded
```

### 7.1 What actually forces playback transcoding

In order of how often it bites, which is also the order of value in fixing it:

1. **Audio.** DTS, DTS-HD, TrueHD, FLAC, PCM.
2. **Subtitles.** Forced PGS tracks auto-selected by the client.
3. **Video, but only genuinely incompatible video.** The encode list in 6.2.
4. **Container.** Rare. MKV and MP4 both fine.

---

## 8. Video bitrate

Applies only when `Kind == full` and to the manual space sweep. Most jobs never
compute a bitrate.

### 8.1 Sample probe

Encode three short samples with a software encoder at fixed quality, measure what
that quality costs for this content, use it as the target.

1. Skip the first and last 5% (credits, black frames, logos).
2. Pick three 60-second segments at 20%, 50% and 80% of the remainder.
3. For each, **encode to a real temp file and stat it.** Do not use `-f null -`;
   it does not reliably report output size.
   ```
   ffmpeg -nostdin -ss <t> -t 60 -i <src> \
     -an -sn -c:v libx265 -crf 21 -preset veryfast \
     -x265-params log-level=none <tmp>/probe_<n>.mkv
   ```
   Delete after stat. Three 1080p segments total roughly 75 MB of temp space.
4. `base = median(sample_bitrates)`
5. **Hardware correction: `target = base * 1.35`.** The UHD 630 fixed-function
   encoder is less efficient than x265 at equal bitrate, and Gen 9.5 is one of
   the weaker QSV generations. This is a policy constant; tune it after the VMAF
   spot-check in section 27 and rebuild.
6. Clamp:
   - `target = min(target, source_video_bitrate * 0.85)` (resolved per 8.4)
   - `target = max(target, floor[resolution])`
   - `target = min(target, ceiling[resolution])`

Run the three segments concurrently; they are independent and this is not a
transcode, so the one-job-at-a-time rule does not apply. Wall time drops to
roughly one segment.

**This runs as the first phase of the job, not at enqueue time.** See 17.2 for
what that means for the transform record.

### 8.2 Fallback formula

If the probe fails: `target_bps = width * height * fps * BPP`

| Resolution | BPP (HEVC) |
| --- | --- |
| <= 1080p | 0.065 |
| 1440p | 0.060 |
| 2160p | 0.055 |

At 23.976 fps: 720p ~1.4 Mbps, 1080p ~3.2 Mbps, 2160p ~10.9 Mbps.

For 50/60 fps scale by `fps/24`, capped at 1.6. Add 25% for HDR / 10-bit.

### 8.3 Floors and ceilings

| Resolution | Floor | Ceiling |
| --- | --- | --- |
| <= 576p | 0.8 Mbps | 2.5 Mbps |
| 720p | 1.5 Mbps | 4 Mbps |
| 1080p | 2.5 Mbps | 8 Mbps |
| 1440p | 4 Mbps | 12 Mbps |
| 2160p | 8 Mbps | 20 Mbps |

Rate control: `-b:v <target> -maxrate <target*1.6> -bufsize <target*2>`

### 8.4 Resolving source video bitrate

ffprobe's per-stream `bit_rate` is usually **absent for Matroska**. Resolve it per
file at analysis time, first match wins:

1. the stream's `bit_rate`
2. the `BPS` / `BPS-eng` statistics tag (written by mkvmerge)
3. `(file_size_bits - sum(known audio bitrates) * duration - 2% container
   allowance) / duration`
4. the format-level overall `bit_rate`, as an upper bound of last resort

Store the resolved value and which rung produced it. It is consumed by exactly
three things - the 0.85 clamp in 8.1, the sweep filter in section 11, and the
library column - so rung 3 accuracy is sufficient. **Copy decisions never look at
bitrate at all** (6.2).

---

## 9. HDR, Dolby Vision, interlacing

A stream is HDR if `color_transfer` is `smpte2084` (PQ) or `arib-std-b67` (HLG),
usually with `color_primaries: bt2020` and `color_space: bt2020nc`. Mastering
display and content light level arrive in `side_data_list`.

HDR sources are already HEVC Main 10, so they are almost always **copied** and
none of this fires. It matters only for an HDR file failing the copy test for
another reason.

**Never tonemap.** Intel UHD 630 (Coffee Lake, Gen 9.5) has fixed-function HEVC
Main10 10-bit encode, and HDR10 metadata passthrough works via `hevc_qsv` and
`hevc_vaapi` (the latter retains HDR SEIs by default). When encoding HDR:

- Profile `main10`, pixel format `p010le` set in the filter chain (14.1), not via
  `-pix_fmt`
- `-color_primaries bt2020 -color_trc smpte2084 -colorspace bt2020nc`
- Preserve mastering display and MaxCLL/MaxFALL

VERIFY: transcode one HDR file on the actual ffmpeg build and confirm the output's
`side_data_list` still carries mastering display and MaxCLL/MaxFALL.

**Dolby Vision profile 5 must never be re-encoded.** Profiles 7 and 8 have an
HDR10-compatible base layer, so losing the RPU degrades gracefully to HDR10.
Profile 5 has no HDR10 base layer; it uses IPT-PQ-C2 colour, and stripping the RPU
produces visibly wrong output, typically green and purple. Detect it and copy the
video stream, downgrading the plan to `audio_only`.

**Copying the stream is necessary but not sufficient.** The Dolby Vision
configuration record (`dvcC`/`dvvC`) and the in-band RPUs must survive the
container rebuild, or players never engage DV mode and profile 5 output renders
wrong even though the video bits are untouched. ffmpeg's mov muxer carries the
record in MP4; Matroska support landed in ffmpeg 6.1, so jellyfin-ffmpeg7
qualifies - VERIFY on the actual build. Verification (15.3) enforces it at
runtime: a profile 5 source whose output lacks the DOVI record fails verification,
staging kept, source untouched. For profiles 7 and 8 the same loss degrades to
HDR10, so it logs a warning instead of failing.

Detect via the DOVI configuration record in ffprobe stream side data. VERIFY the
exact JSON path for the profile number.

HDR10+ dynamic metadata is lost on re-encode, falling back to the HDR10 base
layer. Acceptable. On a copy it survives, since the metadata is SEI NALs inside
the stream.

**Interlaced content** is independent of everything else and the most likely to
produce ugly output if ignored. An explicit interlaced `field_order` (`tt`, `bb`,
`tb`, `bt`) forces an encode with deinterlacing; unknown or absent counts as
progressive, with the `idet` sampling fallback for legacy codecs described in 6.2.

Deinterlace with `vpp_qsv=deinterlace=2` on the hardware-decode path or
`bwdif=mode=send_frame` on the software-decode path. **`bwdif` is a software
filter and cannot run on QSV frames** - putting it on a hardware pipeline is a
silent failure mode.

---

## 10. Hardware

### 10.1 Probe

Compiled-in support is not working support. Test with a real encode at startup and
on demand, for each of `{qsv, vaapi}` x `{hevc main, hevc main10}`:

```
ffmpeg -hide_banner -loglevel error -nostdin \
  -init_hw_device qsv=hw:/dev/dri/renderD128 -filter_hw_device hw \
  -f lavfi -i testsrc=size=640x480:rate=30:duration=1 \
  -vf "format=p010le,hwupload=extra_hw_frames=64" \
  -c:v hevc_qsv -profile:v main10 -f null -
```

Cache results in SQLite with the ffmpeg version. Re-probe when the version
changes. Manual re-probe button in the UI. Test 10-bit separately from 8-bit.

Expected on UHD 630: QSV and VAAPI both working, HEVC Main and Main10 both
available. If Main10 fails, the cause is almost certainly the driver stack rather
than the silicon.

**Decode capability.** The encode probe does not cover decode. Keep a hard-coded
hardware-decodable set for Gen 9.5:

```
{h264, hevc, mpeg2video, vc1, vp9}
```

Everything else - AV1, MPEG-4 ASP / Xvid, WMV and the rest - decodes in software
(14.1). Include a VP9 decode check in the probe run to confirm the driver
delivers it. If a hardware decode fails at runtime anyway (driver quirk,
malformed stream), retry the job once with software decode plus `hwupload`, and
record the fallback on the job the same way encoder fallback is recorded.

### 10.2 Selection

Prefer QSV, fall back to VAAPI, fall back to software (`libx265`).

If it falls back to software, **record it on the job and show it in the UI in a
colour that is hard to ignore.** A silent fallback turns a 20-minute job into a
4-hour one.

---

## 11. Space reclaim sweep (manual only)

The automatic pipeline serves compatibility and barely touches video. This is
where disk space gets reclaimed, deliberately.

**Never runs automatically. Never triggered by ingest.**

Finds files whose video stream is H.264 above 8 Mbps (bitrate resolved per 8.4),
runs the sample probe, and queues a re-encode to HEVC only if the projected saving
exceeds 35%. Evaluated against the probe result rather than a fixed table, so it
adapts per file and spares high-bitrate files that are high-bitrate because the
content is complex.

| Source | HEVC target | Saving |
| --- | --- | --- |
| 1080p Blu-ray remux, 30 Mbps | ~4 Mbps | ~85% |
| 1080p BluRay x264, 10 Mbps | ~3.5 Mbps | ~65% |
| 1080p WEB-DL, 6 Mbps | ~3 Mbps | ~50% |
| 1080p web-rip, 2.5 Mbps | ~2.2 Mbps | ~12%, and it looks worse |

Always dry-run first. The confirmation must show the count plus total projected
saving and state plainly that the operation is irreversible.

---

## 12. Loop prevention

**Non-negotiable.** In-place replacement plus rediscovery equals an infinite
re-encode loop that degrades the library one generation at a time.

Three defences. The first two are about identity, the third about provenance.

1. **Record the output identity at promotion.** Immediately after the rename
   succeeds, compute the fingerprint of the file as written and store it on both
   the job and the media row, along with size, mtime, the producing job id and the
   policy hash in force.

   This is what makes "is this still our output?" answerable, and it is not
   optional - see the skip rule below.

2. **Tag every output.** `-metadata CODARR=1 -metadata CODARR_VERSION=<semver>
   -metadata CODARR_POLICY=<hash>`.

   **The tag alone must never be sufficient to skip a file.** A third party can
   rewrite a file while preserving global tags - Bazarr embedding a subtitle track
   via mkvmerge is the realistic case, and mkvmerge carries global tags through.
   The result is a file that now contains a PGS track Codarr would normally strip,
   still wearing a CODARR tag with a matching policy hash. Trusting the tag alone
   makes that file invisible forever.

   The skip rule is therefore a conjunction:

   ```
   SKIP only if:
     format.tags.CODARR is present
     AND CODARR_POLICY == current policy hash
     AND current fingerprint == codarr_output_fingerprint
   ```

   If the tag matches but the fingerprint does not, the file was modified after
   Codarr wrote it. Re-analyze it properly, re-plan it, and mark its provenance
   `modified_since_transcode` so it is visible in the UI rather than silently
   reprocessed.

   MKV handles arbitrary global tags cleanly. **MP4 does not** - it has a
   restricted tag namespace, so custom keys need `-movflags use_metadata_tags`
   (always present on MP4 output, combined with `+faststart`, 6.1) to land in the
   `udta` atom. VERIFY that a tag written this way reads back via ffprobe on the
   actual build. If it does not survive the round trip, encode the three values
   into the `comment` field as a single structured string and parse it back. Do
   not ship MP4 output without confirming one of these works; an untagged output
   plus in-place replacement is exactly the loop this section exists to prevent.
   The database record below is the backstop if it slips through, but do not rely
   on it alone.

   The policy hash is computed from the Go constants. Changing the policy and
   rebuilding makes previously-tagged files eligible again, which is what
   "re-check all done items" acts on. It must require an explicit user action,
   never fire automatically on startup.

3. **Database record.** `media_files` keyed on absolute path with size, mtime and
   the fingerprint. Unchanged (size, mtime) means no re-analysis, so scans stay
   cheap.

Files that end as `skip` are never written and therefore never tagged or given an
output fingerprint. `codarr_output_fingerprint IS NULL` is exactly the statement
"Codarr never wrote this file". Do not write a file just to tag it.

### 12.1 The fingerprint

One hash function everywhere, so every comparison is apples to apples:
**xxh3-128** over the first 1 MiB, the last 1 MiB, and the exact byte size as an
eight-byte suffix. Use `github.com/zeebo/xxh3` - fast, 128-bit, pure Go, no cgo.

Two reads of 1 MiB, regardless of file size. This is deliberately not a whole-file
hash: media files are tens of gigabytes on NFS, and hashing every one on every
daily scan would read the entire library nightly for no benefit.

What it catches, which is everything that realistically happens to these files: a
replacement, a remux, a subtitle embed, a truncated copy, an *arr manual import.
All of them change size or the head or the tail.

What it does not catch: an in-place edit of interior bytes that preserves length.
For deliberate tampering that is a gap; for accidental corruption ZFS already
checksums every block and scrubs, so duplicating that here would be redundant
work on the same data.

### 12.2 Optional whole-file hash

For users who want a definitive integrity record, a setting (`full_hash_enabled`,
**default off**) additionally computes an xxh3-128 over the entire staging file
after verification and before promotion, storing it on the job and the media row.

The cost is one extra full read of the output. On an `audio_only` job - already
one full read plus one full write - that is roughly 40% more I/O. On a `full` job
it is closer to 5%, since the encode dominates.

**It is computed once and verified only on demand**, never during the daily scan.
Drift detection is the sparse fingerprint's job; the full hash exists so that
`POST /api/media/{id}/verify-integrity` can give a definitive answer when
something looks wrong. Keeping these two jobs separate is what keeps the scan
cheap.

---

## 13. Ingest

Two paths: webhooks for immediacy, a daily scan as the safety net.

**There is no filesystem watcher.** The media lives on NFS (15.6), where inotify
only reports writes made through this client's own mount - imports done by *arrs
running elsewhere are invisible to it. A watcher would suggest coverage it does
not have.

### 13.1 Webhook

```
POST /api/webhook/{webhook_id}
```

One unguessable id **per *arr instance**, generated when the instance is created
in the UI. The UI displays the full URL to paste into that instance's
Settings -> Connect -> Webhook. A per-instance path removes all guessing about
which instance sent an event.

Payload fields actually read:

- **Radarr:** `eventType`, `movie.{id,title,folderPath}`,
  `movieFile.{id,relativePath,path}`, `isUpgrade`
- **Sonarr:** `eventType`, `series.{id,title,path}`, `episodes[]`,
  `episodeFile.{id,relativePath,path}`, `isUpgrade`

Handle `Download`, `Rename`, `Test`. `Test` must return 200 with a body so the
*arr's Test button reports success. Handle `MovieFileDelete` and
`EpisodeFileDelete` by marking the file `missing` (13.2).

The `path` is that instance's view of the filesystem and needs remapping using
**that instance's** mappings (16.2).

### 13.2 Scheduled scan

Daily at a configurable time, default 04:00. Walks every root, compares against
`media_files`, enqueues analysis for anything new or changed. Manually
triggerable. Rate-limited so it does not thrash the array.

Three rules:

- Ignore `.part`, `.!qB`, `.partial`, `.tmp`, dotfiles.
- **Stability guard:** skip any file whose mtime is within the last 2 minutes; the
  next scan catches it. Webhook-imported files are complete when the *arr fires,
  so this only matters for files appearing outside the *arrs - but it is one stat
  field and prevents transcoding a half-copied file.
- **Prune:** any `media_files` row whose path no longer exists is marked status
  `missing` (row and history kept). If the path reappears, re-fingerprint and
  re-analyze. Without this the dashboard's compatibility summary drifts from
  reality.

### 13.3 Exclusions

Hard-coded:

- Plex extras dirs: `Behind The Scenes`, `Deleted Scenes`, `Featurettes`,
  `Interviews`, `Scenes`, `Shorts`, `Trailers`, `Other`
- `*-trailer.*`, `*-sample.*`, `sample.*`
- Files under 50 MB
- Non-video extensions

Plus a per-file ignore list in SQLite, settable from the UI.

---

## 14. Execution

### 14.1 ffmpeg invocation

Build argv programmatically. Never concatenate strings. Log exact argv per job.

**Decode selection.** Hardware decode is conditional, not default:

- Hardware decode (`-hwaccel qsv -hwaccel_output_format qsv`) only when the source
  codec is in the hardware-decodable set (10.1) AND every needed video filter has
  a hardware variant.
- Otherwise software decode, with pixel format and upload handled in the filter
  chain: `-vf format=nv12,hwupload=extra_hw_frames=64` (`format=p010le` for
  Main 10).
- **`-pix_fmt` appears only on the pure-software-encode fallback**, never
  alongside hardware surfaces - it conflicts with QSV frames.
- Deinterlacing: `vpp_qsv=deinterlace=2` (or `deinterlace_vaapi`) on the hardware
  path; `bwdif=mode=send_frame` inserted before `format`/`hwupload` on the
  software-decode path.
- Legacy-container inputs (AVI, VOB, MPG, TS, FLV, ...) get `-fflags +genpts` as
  an input option; their timestamps are routinely broken and otherwise die with
  non-monotonic DTS during the remux.

Passing `-hwaccel qsv` unconditionally fails on exactly the sources the encode
path exists for: Xvid and MPEG-4 ASP have no QSV decoder on this iGPU, and neither
does AV1.

**Dominant case, `audio_only`** - `-c:v copy`, no hardware device needed:

```
ffmpeg -hide_banner -nostdin -y
  -i <source>
  -map 0:v:0  -c:v copy
  -map 0:a:0  -c:a:0 ac3 -b:a:0 640k
  -map 0:a:1  -c:a:1 copy
  -map 0:s:1  -c:s:0 copy
  -map 0:s:2  -c:s:1 srt
  -map_metadata 0
  -map_chapters 0
  -metadata CODARR=1 -metadata CODARR_VERSION=<v> -metadata CODARR_POLICY=<hash>
  -progress pipe:1 -nostats
  <staging>/output.mkv
```

When the level rewrite from 6.2 applies, the copied video stream additionally
carries `-bsf:v h264_metadata=level=4.2`. When the output is MP4:
`-movflags +faststart+use_metadata_tags`, subtitle codec `mov_text`, audio
conversion target AAC, and `-tag:v hvc1` if the video is HEVC.

**`full`, hardware-decode path** (source codec in the hw set: h264, hevc,
mpeg2video, vc1, vp9):

```
ffmpeg -hide_banner -nostdin -y
  -init_hw_device qsv=hw:/dev/dri/renderD128 -filter_hw_device hw
  -hwaccel qsv -hwaccel_output_format qsv
  -i <source>
  -map 0:v:0  -c:v hevc_qsv -profile:v main
              -b:v <target> -maxrate <target*1.6> -bufsize <target*2>
  -map 0:a:0  -c:a:0 ac3 -b:a:0 640k
  -map 0:s:0  -c:s:0 srt
  -map_metadata 0 -map_chapters 0
  -metadata CODARR=1 -metadata CODARR_VERSION=<v> -metadata CODARR_POLICY=<hash>
  -progress pipe:1 -nostats
  <staging>/output.mkv
```

Interlaced sources insert `-vf vpp_qsv=deinterlace=2`.

**`full`, software-decode path** (AV1, Xvid, anything outside the hw set):

```
ffmpeg -hide_banner -nostdin -y
  -init_hw_device qsv=hw:/dev/dri/renderD128 -filter_hw_device hw
  -fflags +genpts
  -i <source>
  -map 0:v:0  -vf format=nv12,hwupload=extra_hw_frames=64
              -c:v hevc_qsv -profile:v main
              -b:v <target> -maxrate <target*1.6> -bufsize <target*2>
  -map 0:a:0  -c:a:0 ac3 -b:a:0 640k
  -map 0:s:0  -c:s:0 srt
  -map_metadata 0 -map_chapters 0
  -metadata CODARR=1 -metadata CODARR_VERSION=<v> -metadata CODARR_POLICY=<hash>
  -progress pipe:1 -nostats
  <staging>/output.mkv
```

`-fflags +genpts` on legacy containers only. Interlaced sources become
`-vf bwdif=mode=send_frame,format=nv12,hwupload=...`. HDR variants use
`format=p010le` in the filter chain, `-profile:v main10` and the colour flags
from section 9.

Mandatory on every job:

- `-nostdin` - otherwise ffmpeg consumes the parent's stdin and hangs
- explicit `-map` for every kept stream - default selection silently keeps one
  stream per type and discards the rest
- `-map_metadata 0` and `-map_chapters 0`
- `-progress pipe:1 -nostats`
- explicit `-disposition:<spec>` for every kept stream

### 14.2 The output stream index trap

**`-c:a:N`, `-b:a:N`, `-metadata:s:a:N`, `-disposition:a:N` and `-bsf:v:N` all
index the OUTPUT stream position, not the source stream.** When subtitle 1 is
dropped, source subtitle 2 becomes output subtitle 1.

Note the `audio_only` example above: source subtitles 1 and 2 are mapped, source 0
is dropped, and the codec options are `-c:s:0` and `-c:s:1` referring to output
positions.

Build the map list first, then assign codec, metadata, disposition and
bitstream-filter options by enumerating that list. Getting this wrong applies
settings to the wrong track and produces a plausible-looking file that is subtly
incorrect.

**This is the single most valuable thing to cover with golden-file tests.**

### 14.3 Progress and duration estimation

Parse `-progress pipe:1` key=value output: `out_time_us`, `frame`, `fps`, `speed`,
`total_size`. Percentage against probed duration.

**Persist progress to the database**, because polling makes the database the only
thing the UI can see. Writing on every ffmpeg progress line would hammer SQLite
for no benefit, so throttle: keep the live value in memory and flush to the job
row every 5 seconds. With a 10-second poll interval that is more than enough
resolution.

Keep the final `out_time_us` of the run in memory; verification uses it for
legacy-container sources (15.3).

Keep the last 200 lines of stderr per job in a ring buffer and persist it on
failure.

**Estimate at enqueue, measure at completion.** Both stored, both shown.

- `audio_only` and `remux`: I/O bound. Estimate `source_bytes * 2 / throughput`,
  where throughput is a rolling average of observed read+write rate from past jobs
  of the same kind.
- `full`: encode bound. Estimate `media_duration / speed_ratio`, where
  `speed_ratio` is a rolling average per (encoder, resolution).

Seed both with conservative defaults until data exists. Store the rolling averages
in `throughput_stats` so estimates improve over time. The enqueue-time estimate
for `full` jobs is necessarily rough because the bitrate probe has not run yet;
refine it when the job starts.

---

## 15. Promotion

### 15.1 Staging location

**Write the output directly to a staging file on the destination filesystem**:
`<dest_dir>/.codarr-staging-<job_id>.mkv`

This matters more than it looks. `audio_only` jobs are I/O bound and dominate the
queue. Writing to a temp volume and then copying to the destination means reading
the source from the array, writing to temp, reading from temp, writing to the
array - **double the array I/O** of writing straight to the destination, where
promotion is then just an atomic `rename()`.

Fall back to the configured temp directory only when the destination filesystem
lacks space. In that case promotion requires a copy to a destination-side staging
file first, because `rename(2)` is not atomic across filesystems.

The staging file is a dotfile, so the *arrs and Plex ignore it.

### 15.2 Sequence

1. Preflight (15.4)
2. Encode to the staging file
3. Verify (15.3). On failure, keep the staging file for inspection and fail.
4. **Check Plex for an active stream on the target path.** If streaming, move the
   job to `awaiting_stream_end` and retry every 60 seconds. The staging file just
   sits there.
5. `fsync` the staging file and its parent directory
6. **Re-check Plex immediately before the rename** to shrink the race window
7. `rename()` staging over the original path - atomic replace. The original inode
   is freed here, immediately and irreversibly.
8. Restore mode, mtime, and (best effort) uid and gid from the original
9. **Record the output identity** (12): fingerprint, size, mtime, job id, policy
   hash, and the full hash if enabled. Do this after the metadata restore in step
   8, since restoring mtime changes what a later scan will compare against.
10. Notify Plex and the owning *arr instance

**There is no trash and no undo.** Once step 7 completes, the source file is gone.
Everything that protects against a bad outcome happens *before* it - see 15.5.

On startup, sweep for orphaned `.codarr-staging-*` files and stale temp
directories left by a crash, after the job-state sweep in 19.2 has claimed what it
can.

**On mtime:** preserve it so other tools sorting by modification time behave
sanely and so nothing downstream mistakes a rewrite for a new file. Plex's
"recently added" ordering uses its own `addedAt` field rather than filesystem
mtime, so this does not affect Plex ordering either way.

### 15.3 Verification

- ffprobe succeeds on the output
- duration within 1% of source, with a legacy fallback: container headers on VOB,
  AVI and friends routinely lie about duration - exactly the remux inputs. If the
  source is a legacy container and the 1% check against the probed source duration
  fails, compare the output's duration against ffmpeg's own final `out_time` from
  the progress stream instead (within 1%); pass on that, and log the source
  mismatch as a warning. No extra decode pass - ffmpeg's `out_time` is ground
  truth for what it wrote.
- expected stream count and types per the plan
- every expected audio language present
- at least one audio stream exists
- **video codec, profile, level and resolution unchanged when the plan said
  copy** - a copy that silently re-encoded is a bug worth catching. Level is
  exempt exactly when the plan recorded a level rewrite; verify the output level
  is 4.2 instead.
- **the DOVI configuration record is present in the output whenever the source
  carried one: hard failure for profile 5, warning for profiles 7/8** (section 9)
- **output not larger than source, for `full` plans only**

The size check applies only to `full`. An `audio_only` plan can legitimately grow
a file when a 1.5 Mbps DTS track becomes 640k AC3 while video is untouched.

### 15.4 Preflight

- Destination filesystem free space at least 1.2x source size (or temp, if falling
  back)
- Source still exists and is unchanged since analysis
- Destination directory writable
- Staging directory and destination directory report the same device number
  (15.6)
- **`st_nlink == 1`.** If greater, fail with a clear error. One stat call, and it
  prevents damaging a hardlinked seeding copy if the setup ever changes.

### 15.5 There is no undo

Promotion destroys the original. No trash directory, no retention window, no
restore path. This keeps the promote sequence to a single atomic `rename()` and
removes the double-occupancy problem where every replaced file would consume both
the old and new copy until a purge ran.

The cost is real: **a policy that turns out to be wrong is applied irreversibly
across every file it touches.** Three things carry that weight instead, and none
of them are optional:

**Verification before promotion (15.3) is the last line of defence.** With no
undo, a check that would previously have been recoverable is now the only thing
standing between a bad encode and a destroyed source. Do not weaken it, and do not
add a "force promote despite verification failure" escape hatch.

**Dry run is mandatory on anything touching more than one file.** Re-check all,
re-encode selected, and the space sweep must all preview first, show the exact
count and the plan kind breakdown, and require explicit confirmation. The
confirmation copy must state plainly that the operation is irreversible.

**The *arrs are the actual recovery mechanism.** If a transcode ruins a file,
Radarr or Sonarr can fetch it again. This is why trash is expendable here in a way
it would not be for irreplaceable data: the library is reproducible. Put this in
the README and the UI help text so the reasoning is visible later.

A corollary for testing: **phase 3 in the build order writes to a scratch
directory rather than over the source, and that phase exists precisely because
there is no undo.** Do not collapse it into phase 4 to save time.

### 15.6 NFS

The media lives on an NFSv4.2 mount, a single dataset. Three consequences.

**`rename()` is atomic server-side, and staging is always a sibling.** NFSv4
`RENAME` is a single server-side operation, so the replace in 15.2 step 7 is
atomic from the server's point of view. The staging file is a dotfile in the
destination directory, guaranteeing it is on the same filesystem as its target, so
`rename()` never crosses a filesystem boundary and never returns `EXDEV`. Assert
it in preflight by comparing device numbers rather than trusting it: NFSv4
presents all exports inside one pseudo-filesystem, so if the mount is ever split
into separate datasets the client-side paths look unchanged while the assumption
silently stops holding.

**Replacing a file that another client has open causes `ESTALE`, not graceful
continuation.** On a local filesystem, `rename()` over an open file leaves the
reader on the old inode and playback continues. **This does not hold on NFS.** The
server generates a new file handle for the replacement, the reading client's cached
handle no longer resolves, and it gets `ESTALE` - a hard failure, not a
degradation. The behaviour also varies by backing filesystem; it has been reported
on ZFS where ext4 and XFS did not show it, because of how each encodes inode
identity into file handles.

**Therefore the Plex active-stream guard is essential, not defensive:**

- The re-check immediately before `rename()` in step 6 is load-bearing. Keep the
  window between that check and the rename as small as possible - no logging, no
  database writes, no allocation between them.
- If a stream starts inside that window, the user's playback dies outright. An
  accepted, very small risk, but a real one; state it in the UI help text rather
  than let it be discovered.

**`chown` may fail under `root_squash`.** Restoring uid and gid requires privilege
a squashed export does not grant. Attempt it, log a warning on failure, and **do
not fail the job**. Mode and mtime do not need privilege and should still succeed.
If ownership consistently fails, the export needs `no_root_squash` or the
container needs to run as the owning uid - surface that as a one-time warning
rather than a per-job error.

---

## 16. Plex and the *arrs

### 16.1 Plex - one instance only

After a replacement, two calls in order:

1. **Partial scan of the containing directory:**
   `GET /library/sections/{id}/refresh?path=<url-encoded Plex-side directory>`
   Resolve `{id}` from `GET /library/sections`, which lists each section with its
   `Location` paths. Cache that mapping.
2. **Analyze the item:** `PUT /library/metadata/{ratingKey}/analyze`

**Never delete and rescan.** That destroys watch state, ratings, playlist
membership and collections. `analyze` updates media info without touching any of
it.

VERIFY the exact verb and path for `analyze` on the running PMS version.

**Active stream guard.** `GET /status/sessions` with `X-Plex-Token` and
`Accept: application/json`. Extracting the file path is not straightforward: a
direct play session's `Part` element carries a `file` attribute, but a transcoding
session's does not. Robust approach: take `ratingKey` from each session, then
`GET /library/metadata/{ratingKey}` and read `Media[].Part[].file`. Cache briefly.
Reverse the path mapping before comparing.

**Do not pause the queue while Plex is streaming.** The iGPU handles a Plex
transcode and a Codarr encode concurrently, and the array handles the I/O. Build
no pause-on-stream setting at all. The per-file guard in rule 7 is separate and
stays: never *replace* a file being streamed right now. That is about not swapping
a file out from under a reader, not about resource contention.

**Authentication.** Accept a token entered in the UI. Also implement the plex.tv
PIN flow:

1. Generate a stable `X-Plex-Client-Identifier` UUID once and persist it.
2. `POST https://plex.tv/api/v2/pins?strong=true` with
   `X-Plex-Client-Identifier`, `X-Plex-Product: Codarr`,
   `Accept: application/json`. Returns `id` and `code`.
3. Open `https://app.plex.tv/auth#?clientID=<id>&code=<code>&context[device][product]=Codarr`
4. Poll `GET https://plex.tv/api/v2/pins/{id}` until `authToken` is non-null.
5. Persist the token.

Plex also has a newer JWT flow that expires every 7 days. For an unattended service
the legacy long-lived `X-Plex-Token` is correct.

### 16.2 The *arrs - multiple instances

**There are several Radarr and Sonarr instances, currently two each.** The data
model, path mapping, attribution and UI must all be per-instance from the start.
Do not build for one and generalise later.

The common reason for two of each is a 1080p instance and a 4K instance with
separate root folders and quality profiles. The same title may exist twice at
different paths, managed by different instances. Both are processed independently,
which is correct.

Each instance has: name, flavour (`radarr` or `sonarr`), base URL, API key, its
own `webhook_id`, its own path mappings, its own root folders, an enabled flag and
a last test result.

**Root folders and attribution.** Offer "import root folders" per instance,
calling `GET /api/v3/rootfolder`, which creates `roots` rows linked to that
instance. Roots may also be added manually without an instance, in which case files
there are processed but no *arr is notified.

Attribute a file to an instance by **longest-prefix match** on root path. This
handles nested roots correctly.

**If two enabled instances claim the same root path, that is a configuration
error.** Show it prominently in the UI, and process files there without *arr
notification rather than guessing. Do not silently pick one.

**After a replacement**, notify the owning instance:

- Radarr: `POST /api/v3/command {"name":"RescanMovie","movieId":<id>}`
- Sonarr: `POST /api/v3/command {"name":"RescanSeries","seriesId":<id>}`

**Re-grab risk is closed operationally: upgrades are disabled on every instance**
(confirmed; see 23.2). With upgrades off, a rescan cannot trigger an upgrade search
regardless of what the refreshed mediainfo says. Two rules still apply:

- **Codarr never renames.** The filename may end up carrying stale codec info after
  a `full` job - an `x264` tag on a now-HEVC file. That is cosmetic: quality was
  parsed from the release name at import and is not re-derived from the filename,
  and Plex reads streams, not names. Keep the stem identical; only the extension
  changes when a legacy container becomes MKV.
- *arr renaming must stay off, or its naming format must use no `{MediaInfo ...}`
  tokens - otherwise a later manual rename pass would churn paths for no benefit
  (23.2).

**Optional, default off: `unmonitor_after` per instance.** After a successful
`full` job, unmonitor the item so it can never be re-grabbed even if upgrades are
re-enabled later. Radarr: fetch the movie and `PUT /api/v3/movie` with
`monitored=false`. Sonarr: `PUT /api/v3/episode/monitor` with the episode ids from
the webhook payload. Belt-and-braces, unnecessary while upgrades stay off, so it
ships off by default and late (phase 8).

---

## 17. Data model

SQLite, WAL mode, `foreign_keys=ON`, `busy_timeout=5000`. Embedded migrations. Use
`modernc.org/sqlite` (pure Go) so the binary stays CGO-free.

**Concurrency note:** WAL allows many readers and one writer, but Go's
`database/sql` pool can still produce `SQLITE_BUSY` under concurrent writes from
the HTTP handlers and the worker. Use a **single dedicated write connection**
(`SetMaxOpenConns(1)` on a write pool) alongside a separate read pool, or serialise
writes behind the store. Cover this with an integration test that writes from
several goroutines.

### 17.1 Schema

```sql
-- Configuration, edited via the UI. Single row.
CREATE TABLE settings (
  id                       INTEGER PRIMARY KEY CHECK (id = 1),
  temp_dir                 TEXT NOT NULL,
  qsv_device               TEXT NOT NULL DEFAULT '/dev/dri/renderD128',
  scan_enabled             INTEGER NOT NULL DEFAULT 1,
  scan_cron                TEXT NOT NULL DEFAULT '0 4 * * *',
  scan_rate_limit_fps      INTEGER NOT NULL DEFAULT 50,
  queue_paused             INTEGER NOT NULL DEFAULT 0,
  prioritise_quick_jobs    INTEGER NOT NULL DEFAULT 1,
  full_hash_enabled        INTEGER NOT NULL DEFAULT 0,   -- 12.2
  updated_at               TIMESTAMP NOT NULL
);

CREATE TABLE plex (
  id                    INTEGER PRIMARY KEY CHECK (id = 1),
  base_url              TEXT NOT NULL,
  token                 TEXT,
  client_identifier     TEXT NOT NULL,
  refresh_after         INTEGER NOT NULL DEFAULT 1,
  analyze_after         INTEGER NOT NULL DEFAULT 1,
  guard_active_streams  INTEGER NOT NULL DEFAULT 1,
  last_tested_at        TIMESTAMP,
  last_test_result      TEXT,
  updated_at            TIMESTAMP NOT NULL
);

CREATE TABLE plex_path_mappings (
  id     INTEGER PRIMARY KEY,
  local  TEXT NOT NULL,
  remote TEXT NOT NULL,
  sort   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE arr_instances (
  id               INTEGER PRIMARY KEY,
  name             TEXT NOT NULL UNIQUE,
  flavour          TEXT NOT NULL,        -- radarr | sonarr
  base_url         TEXT NOT NULL,
  api_key          TEXT NOT NULL,
  webhook_id       TEXT NOT NULL UNIQUE,
  rescan_after     INTEGER NOT NULL DEFAULT 1,
  unmonitor_after  INTEGER NOT NULL DEFAULT 0,
  enabled          INTEGER NOT NULL DEFAULT 1,
  last_tested_at   TIMESTAMP,
  last_test_result TEXT,
  created_at       TIMESTAMP NOT NULL,
  updated_at       TIMESTAMP NOT NULL
);

CREATE TABLE arr_path_mappings (
  id              INTEGER PRIMARY KEY,
  arr_instance_id INTEGER NOT NULL REFERENCES arr_instances(id) ON DELETE CASCADE,
  local           TEXT NOT NULL,
  remote          TEXT NOT NULL,
  sort            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE roots (
  id              INTEGER PRIMARY KEY,
  path            TEXT NOT NULL UNIQUE,
  arr_instance_id INTEGER REFERENCES arr_instances(id) ON DELETE SET NULL,
  imported        INTEGER NOT NULL DEFAULT 0,
  enabled         INTEGER NOT NULL DEFAULT 1,
  created_at      TIMESTAMP NOT NULL
);

CREATE TABLE media_files (
  id                 INTEGER PRIMARY KEY,
  path               TEXT NOT NULL UNIQUE,
  root_id            INTEGER REFERENCES roots(id) ON DELETE SET NULL,
  arr_instance_id    INTEGER REFERENCES arr_instances(id) ON DELETE SET NULL,
  arr_entity_id      INTEGER,
  size_bytes         INTEGER NOT NULL,
  mtime              INTEGER NOT NULL,
  nlink              INTEGER,
  fingerprint        TEXT,
  probe_json         TEXT,          -- full ffprobe output
  media_info_json    TEXT,          -- parsed summary for the UI modal
  analyzed_at        TIMESTAMP,
  plan_json          TEXT,
  plan_kind          TEXT,
  plan_reasons       TEXT,          -- JSON array of strings
  container          TEXT,
  video_codec        TEXT,          -- denormalised for library filtering
  video_profile      TEXT,
  video_level        TEXT,
  video_bitrate      INTEGER,
  video_bitrate_src  TEXT,          -- which rung of 8.4 produced it
  is_hdr             INTEGER NOT NULL DEFAULT 0,
  fingerprint_algo   TEXT,          -- 'xxh3-128' - lets the algorithm change later
  codarr_tagged      INTEGER NOT NULL DEFAULT 0,
  codarr_policy_hash TEXT,
  -- Output identity, written at promotion (section 12). NULL means Codarr has
  -- never written this file.
  codarr_job_id            INTEGER REFERENCES jobs(id) ON DELETE SET NULL,
  codarr_processed_at      TIMESTAMP,
  codarr_output_fingerprint TEXT,
  codarr_output_size       INTEGER,
  codarr_output_mtime      INTEGER,
  codarr_output_full_hash  TEXT,     -- only when full_hash_enabled
  provenance         TEXT NOT NULL DEFAULT 'untouched',
                                    -- untouched | codarr_output
                                    -- | modified_since_transcode
  integrity_checked_at TIMESTAMP,
  status             TEXT NOT NULL, -- new|analyzed|queued|processing|done
                                    -- |failed|ignored|skipped|missing
  ignored            INTEGER NOT NULL DEFAULT 0,
  last_error         TEXT,
  created_at         TIMESTAMP NOT NULL,
  updated_at         TIMESTAMP NOT NULL
);
CREATE INDEX idx_media_status ON media_files(status);
CREATE INDEX idx_media_plan_kind ON media_files(plan_kind);
CREATE INDEX idx_media_video_codec ON media_files(video_codec);
CREATE INDEX idx_media_instance ON media_files(arr_instance_id);
CREATE INDEX idx_media_provenance ON media_files(provenance);

CREATE TABLE jobs (
  id                 INTEGER PRIMARY KEY,
  media_file_id      INTEGER NOT NULL REFERENCES media_files(id),
  kind               TEXT NOT NULL,
  origin             TEXT NOT NULL,  -- ingest | manual | recheck | space_sweep
  priority           INTEGER NOT NULL DEFAULT 100,
  state              TEXT NOT NULL,
  attempt            INTEGER NOT NULL DEFAULT 0,

  transform_json     TEXT NOT NULL,  -- the history record, see 17.2

  staging_path       TEXT,
  used_temp_dir      INTEGER NOT NULL DEFAULT 0,
  ffmpeg_argv        TEXT,
  probe_result       TEXT,
  progress_pct       REAL,
  progress_speed     REAL,
  estimated_seconds  INTEGER,
  actual_seconds     INTEGER,
  encoder_used       TEXT,
  decode_path        TEXT,           -- hardware | software
  fell_back          INTEGER NOT NULL DEFAULT 0,
  fallback_reason    TEXT,
  source_size        INTEGER,
  output_size        INTEGER,
  output_fingerprint TEXT,           -- recorded at promotion, section 12
  output_full_hash   TEXT,           -- only when full_hash_enabled
  blocked_by         TEXT,
  failure_code       TEXT,           -- 19.1, NOT NULL whenever state='failed'
  failure_message    TEXT,           -- always populated on failure
  stderr_tail        TEXT,
  queued_at          TIMESTAMP NOT NULL,
  started_at         TIMESTAMP,
  finished_at        TIMESTAMP
);
CREATE INDEX idx_jobs_state ON jobs(state, priority, queued_at);
CREATE INDEX idx_jobs_media ON jobs(media_file_id);
-- One active job per file: enqueue is idempotent.
CREATE UNIQUE INDEX idx_jobs_one_active_per_file ON jobs(media_file_id)
  WHERE state IN ('queued','running','verifying',
                  'awaiting_stream_end','promoting');

CREATE TABLE hw_capabilities (
  id             INTEGER PRIMARY KEY,
  backend        TEXT NOT NULL,
  codec          TEXT NOT NULL,
  profile        TEXT NOT NULL,
  direction      TEXT NOT NULL,   -- encode | decode
  works          INTEGER NOT NULL,
  error          TEXT,
  ffmpeg_version TEXT,
  probed_at      TIMESTAMP NOT NULL
);

CREATE TABLE throughput_stats (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  encoder    TEXT,
  resolution TEXT,
  samples    INTEGER NOT NULL,
  avg_value  REAL NOT NULL,
  updated_at TIMESTAMP NOT NULL
);

CREATE TABLE events (
  id            INTEGER PRIMARY KEY,
  level         TEXT NOT NULL,
  category      TEXT NOT NULL,
  message       TEXT NOT NULL,
  media_file_id INTEGER,
  job_id        INTEGER,
  created_at    TIMESTAMP NOT NULL
);
```

Enqueue inserts with `ON CONFLICT DO NOTHING` against the partial unique index; a
duplicate attempt (webhook plus manual trigger, a double click, a re-check
overlapping ingest) is a silent no-op that logs an event.

On successful promotion, update the media row's size, mtime and fingerprint so the
next scan sees the file as unchanged instead of re-probing our own output, and set
the `codarr_output_*` columns plus `provenance = 'codarr_output'`.

`provenance` is derived, not user-set, and is recomputed on every analysis:

| Condition | `provenance` |
| --- | --- |
| `codarr_output_fingerprint IS NULL` | `untouched` |
| current fingerprint == `codarr_output_fingerprint` | `codarr_output` |
| otherwise | `modified_since_transcode` |

It is denormalised onto the row so the library table can filter on it without
recomputing. A file at `modified_since_transcode` is the interesting one: someone
or something rewrote a file Codarr produced. Surface it, do not hide it, and never
let the CODARR tag alone skip it (section 12).

### 17.2 The transform record

`jobs.transform_json` is the before/after history. Written at enqueue with `after`
holding the *plan*, updated at completion with `after` holding the *measured*
result taken from an ffprobe of the output. One schema serves both, which is what
lets the UI render queued and completed items with the same component.

```json
{
  "container": { "before": "matroska", "after": "matroska" },
  "video": {
    "action": "copy",
    "reason": "h264 High L4.0 8-bit 4:2:0 progressive",
    "before": { "codec": "h264", "profile": "High", "level": "4.0",
                "width": 1920, "height": 1080, "fps": 23.976,
                "bitrate_kbps": 8420, "pix_fmt": "yuv420p",
                "hdr": false, "scan": "progressive" },
    "after":  { "codec": "h264", "profile": "High", "level": "4.0",
                "width": 1920, "height": 1080, "fps": 23.976,
                "bitrate_kbps": 8420, "pix_fmt": "yuv420p",
                "hdr": false, "scan": "progressive" }
  },
  "audio": [
    { "source_index": 0, "output_index": 0, "language": "eng",
      "title": "Surround 5.1", "action": "encode",
      "reason": "dts not in copy list for 3+ channels",
      "before": { "codec": "dts", "profile": "DTS-HD MA", "channels": 6,
                  "layout": "5.1", "bitrate_kbps": 1509 },
      "after":  { "codec": "ac3", "channels": 6, "layout": "5.1",
                  "bitrate_kbps": 640 } },
    { "source_index": 1, "output_index": 1, "language": "ger", "title": null,
      "action": "copy", "reason": "aac, stereo",
      "before": { "codec": "aac", "channels": 2, "layout": "stereo",
                  "bitrate_kbps": 192 },
      "after":  { "codec": "aac", "channels": 2, "layout": "stereo",
                  "bitrate_kbps": 192 } }
  ],
  "subtitles": [
    { "source_index": 0, "output_index": null, "language": "eng",
      "action": "drop", "reason": "image-based",
      "before": { "codec": "hdmv_pgs_subtitle", "forced": true },
      "after": null },
    { "source_index": 1, "output_index": 0, "language": "eng",
      "action": "copy",
      "before": { "codec": "subrip", "forced": false },
      "after":  { "codec": "subrip", "forced": false } },
    { "source_index": 2, "output_index": 1, "language": "swe",
      "action": "convert", "reason": "ass to srt",
      "before": { "codec": "ass", "forced": false },
      "after":  { "codec": "subrip", "forced": false } }
  ],
  "attachments": { "before": 3, "after": 0 },
  "chapters": { "before": 12, "after": 12 },
  "size": { "before_bytes": 9871234567, "after_bytes": 8102938475 },
  "duration_seconds": { "estimated": 240, "actual": 218 },
  "output_identity": {
    "fingerprint": "xxh3-128:9f2c...",
    "full_hash": null,
    "size_bytes": 8102938475,
    "mtime": 1735689600,
    "policy_hash": "a41f9c22",
    "recorded_at": "2026-02-14T03:22:11Z"
  }
}
```

Rules:

- Write at enqueue with `after` as prediction and `actual` null.
- Carrying both `source_index` and `output_index` is deliberate: it is the record
  of the mapping described in 14.2 and makes the trap debuggable.
- A level rewrite (6.2) records `action: copy`, the rewrite in `reason`, and the
  new level in `after.video.level`.
- For `full` jobs the target bitrate is unknown at enqueue because the sample probe
  has not run. Leave `after.video.bitrate_kbps` null and have the UI show
  "calculating"; fill it in when the job starts.
- Update at completion with measured values, real sizes, actual duration.
- `output_identity` is null until promotion succeeds, then filled from step 9 of
  15.2. It is the immutable record of what Codarr actually produced; the media
  row's copy is the mutable current-state view, and the two diverging is precisely
  the signal that something rewrote the file.
- On failure, keep the record with predicted values and set the failure fields.
- Never delete transform records. This is the history.

---

## 18. Frontend

Vite + React + TypeScript, built to `web/dist`, embedded. Dark theme. Types and
client generated from `api/openapi.yaml`.

### 18.1 Dashboard

- **Current job:** filename, plan kind, progress bar, fps, speed, elapsed,
  estimated remaining, encoder and decode path in use with a loud warning on
  software encoder fallback, cancel button
- **Queue:** ordered, each row showing plan kind and estimated duration, plus an
  attempt badge when a job was auto-requeued after an interruption
- **Awaiting stream end:** which file, which Plex session blocks it, how long
- **Recent completions:** before/after size and delta, actual duration
- **Failures** needing attention
- **Stats:** total space saved, files done, encode hours
- **Compatibility summary:** how many files still need work, broken down by reason
  (audio, subtitles, video). This is the number that tracks progress toward the
  primary goal.

### 18.2 Library table

Server-side filter, sort, pagination over `media_files`.

Columns: title, source instance, container, video codec/profile/level/resolution/
bitrate, HDR flag, audio codecs and channels, subtitle codecs, size, plan kind,
status, provenance.

Filter by plan kind, video codec, ***arr instance*** and **provenance**. With
several instances, "show me everything from radarr-4k" gets used; so does "show me
everything that changed after Codarr wrote it", which is the view that surfaces a
Bazarr subtitle embed or a manual import quietly undoing this tool's work.

Multi-select with "re-encode selected" and "select all matching filter".

### 18.3 The detail modal

**Clicking any row opens a modal.** It must work for queued, in-progress and
completed items, rendered from `transform_json`.

- Filename and full path, owning *arr instance
- **Before / after comparison**, one section each for video, audio (per track),
  subtitles (per track), container. For queued items `after` is the planned target;
  for done items it is what was produced. **Label clearly which it is.**
- Each row shows the action (copy / encode / convert / drop) and the reason
- File size before and after, with the delta
- Duration: estimated when queued, estimated and actual when done
- Encoder and decode path used, and whether either fell back
- For failures, the `failure_code` as a readable label plus the full
  `failure_message`, and the ffmpeg stderr tail where relevant. An interrupted job
  should say so plainly, with a Retry button.
- **Provenance:** whether this file is still byte-identical to what Codarr wrote,
  which job produced it and when, and the recorded fingerprint. When it reads
  `modified_since_transcode`, say plainly that something rewrote the file after
  Codarr produced it, and show both fingerprints.
- A collapsed technical section with the raw ffmpeg argv and full ffprobe output

Keep the visible media info at reasonable detail: codec, profile, level,
resolution, frame rate, bitrate, channels, layout, language, disposition flags. Not
a full dump.

### 18.4 Settings pages

All configuration is edited here, since there is no config file.

- **General:** temp dir, QSV device, scan settings, queue behaviour
- **Plex:** base URL, token (PIN flow or paste), path mappings with a resolver that
  shows how a given local path translates, library list, Test button
- ***arr instances:** a list supporting **several Radarr and Sonarr instances**.
  Add, edit, enable, delete. Per instance: name, flavour, URL, API key, Test
  button, webhook URL to copy, path mappings, and an "import root folders" action.
  Show a clear error when two enabled instances claim the same root.
- **Roots:** all watch roots with their owning instance, manual add
- **Policy:** read-only display of the hard-coded rules, from `GET /api/policy`
- **Hardware:** probe results, re-probe button, remediation text

**Secrets in the API.** API keys and the Plex token are stored in the database in
plaintext, matching what the *arrs themselves do. Never return them in `GET`
responses - return a masked placeholder, and on `PUT` only overwrite when a
non-placeholder value is supplied. Never log them.

### 18.5 Logs

Read from `GET /api/events`, filterable by level and category, cursor-paginated.
Auto-scroll when the user is at the bottom of the list, freeze when they have
scrolled up.

### 18.6 Polling, not streaming

**The UI polls. There is no SSE and no WebSocket.** Multiple gateway, ingress and
load balancer hops make long-lived connections unreliable: idle timeouts cut them,
buffering proxies defeat the point, and reconnection logic becomes a source of
confusing bugs. Plain request/response survives all of it.

- Poll `GET /api/dashboard` every **10 seconds** while the dashboard is open. One
  call returns the current job, queue, recent completions, failures and stats, so
  the interval costs one request rather than six.
- Poll `GET /api/events?since_id=<last>` every 10 seconds on the logs page.
- **Stop polling when the tab is hidden** (`visibilitychange`) and fire one
  immediate poll when it becomes visible again.
- Poll immediately after any mutation (cancel, retry, queue) rather than waiting
  for the next tick.
- Static pages - settings, hardware, policy - do not poll at all.

Ten seconds is comfortable resolution for work measured in minutes. Do not make it
configurable.

---

## 19. Queue

Single worker, one job at a time.

```
queued -> running -> verifying -> awaiting_stream_end -> promoting -> done
                  \-> cancelled
                  \-> failed
```

Controls:

- **Pause queue.** No new jobs start; a running job continues.
- **Resume queue.**
- **Cancel running job.** SIGTERM, grace period, SIGKILL. Staging file cleaned. The
  job moves to `cancelled` and stays visible at the top of the list. If the queue
  is not paused, the next job starts immediately.
- **Restart a cancelled job.** Re-queues it with priority placing it next, ahead of
  everything already queued.

Model with an integer `priority`, lower runs first. Normal enqueues get 100. A
restarted cancelled job gets `min(current queued priorities) - 1`. Give
`audio_only` and `remux` a better default priority than `full` so quick wins clear
first.

Bulk operations, all dry-run first with an explicit "queue these N jobs"
confirmation:

- **Re-check all done items.** Re-probe every `done` file, recompute the plan
  against the current policy, queue anything that no longer matches.
- **Re-encode selected items.** Same, restricted to a selection. Re-probes first
  and skips anything already correct, so selecting everything is safe.
- **Space reclaim sweep.** Section 11.

### 19.1 Failure states always carry a reason

`failed` is never a bare state. Every failed job stores a machine-readable
`failure_code` and a human-readable `failure_message`, and the UI shows both. Treat
a failed job with an empty message as a bug.

| `failure_code` | Meaning |
| --- | --- |
| `interrupted` | Interrupted repeatedly; the automatic re-queue cap was reached (19.2) |
| `preflight_failed` | Space, `nlink > 1`, device mismatch, missing source, unwritable destination |
| `probe_failed` | ffprobe or the bitrate sample probe errored |
| `ffmpeg_failed` | Non-zero exit from the encode |
| `verification_failed` | An output check in 15.3 did not pass, named specifically |
| `promote_failed` | Staging, rename or permission restore failed |
| `internal_error` | Anything else, with the Go error text |

`failure_message` must be specific enough to act on: not "verification failed" but
"output duration 4382s differs from source 5121s by more than 1%". Include the
ffmpeg stderr tail on `ffmpeg_failed`.

### 19.2 Interrupted jobs

**The pod or node can die mid-transcode.** On startup, every job left in a
non-terminal in-flight state is by definition interrupted, since there is only one
worker process. Restart them automatically - node drains, redeploys and power
events are transient and should not need a click.

| State found at startup | Action |
| --- | --- |
| `running` | Delete the staging file, re-queue at the front (`priority = min(queued) - 1`), `attempt + 1`. |
| `verifying` | Same as `running`. |
| `promoting` | Consistency check below, then re-queue rather than fail. |
| `awaiting_stream_end` | Try to resume. See below. |

**The loop guard.** If something systematically kills the process mid-encode - OOM,
a node with a failing disk, an ffmpeg segfault on a particular file - unlimited
auto-requeueing turns it into a loop that burns the array forever. Automatic
restarts stop at `attempt >= 3`: the job fails with `failure_code = interrupted`
and a message stating how many times it was interrupted. Manual retry resets the
counter.

**`promoting` needs a consistency check**, because it is the only state where the
library file itself may be mid-change. Promotion is a single `rename()`, so exactly
one of two things is true:

- The rename did not happen: the original is intact and the staging file is
  orphaned. Delete the staging file and re-queue (subject to the attempt cap).
- The rename happened: the job actually succeeded. Detect this by checking the
  destination file for the CODARR tag with the current policy hash. If present,
  mark the job `done` and run the Plex and *arr notifications that never fired.

Distinguishing them is exactly why the CODARR tag is checked rather than inferred
from timestamps.

**`awaiting_stream_end` is resumable and worth resuming.** The expensive work is
finished and the staging file is a verified output sitting on the destination
filesystem. On startup, if it still exists and passes verification again, return the
job to `awaiting_stream_end` and carry on. Otherwise delete it and re-queue
(attempt cap applies).

After the state sweep, delete any `.codarr-staging-*` file that no job claims, plus
any stale temp directory.

---

## 20. API

Defined in `api/openapi.yaml`, served at `/api`.

```
GET    /api/health
GET    /api/ready
GET    /api/version
GET    /api/policy                    hard-coded policy, for display

GET    /api/settings
PUT    /api/settings

GET    /api/plex
PUT    /api/plex
POST   /api/plex/test
GET    /api/plex/libraries
POST   /api/plex/resolve-path         {path} -> mapped path
POST   /api/plex/auth/start
GET    /api/plex/auth/poll/{pin_id}

GET    /api/arr                       list all instances
POST   /api/arr                       create instance
GET    /api/arr/{id}
PUT    /api/arr/{id}
DELETE /api/arr/{id}
POST   /api/arr/{id}/test
GET    /api/arr/{id}/rootfolders      live query
POST   /api/arr/{id}/import-roots

GET    /api/roots
POST   /api/roots
DELETE /api/roots/{id}
POST   /api/roots/{id}/scan

GET    /api/media                     q, status, plan_kind, video_codec,
                                      arr_instance_id, sort, page
GET    /api/media/{id}
POST   /api/media/{id}/analyze
POST   /api/media/{id}/queue
POST   /api/media/{id}/ignore
DELETE /api/media/{id}/ignore
POST   /api/media/recheck-all         dry run unless confirm=true
POST   /api/media/recheck-selected
POST   /api/media/{id}/verify-integrity   recompute fingerprint (and full hash
                                          when recorded), compare, update
                                          provenance
POST   /api/media/verify-integrity        body {ids: []}, same for a selection

POST   /api/space-sweep/preview
POST   /api/space-sweep/run           requires confirm=true

GET    /api/jobs
GET    /api/jobs/{id}                 includes transform_json
POST   /api/jobs/{id}/cancel
POST   /api/jobs/{id}/restart

GET    /api/queue
POST   /api/queue/pause
POST   /api/queue/resume

GET    /api/hardware
POST   /api/hardware/probe

POST   /api/webhook/{webhook_id}      *arr receiver

GET    /api/stats
GET    /api/events                    level, category, since_id, limit

GET    /api/dashboard                 single call for the polled dashboard:
                                      current job, queue, recent completions,
                                      failures, stats, compatibility summary

GET    /metrics                       prometheus, outside /api
```

---

## 21. Bootstrap configuration

Everything else lives in the database.

```
--db          / CODARR_DB          /data/codarr.db
--listen      / CODARR_LISTEN      :8080
--log-level   / CODARR_LOG_LEVEL   info
--ffmpeg      / CODARR_FFMPEG      ffmpeg
--ffprobe     / CODARR_FFPROBE     ffprobe
```

**There is no authentication.** Access is secured externally. Build no login, no
API key check, no session handling, no auth middleware. Do not add a "just in case"
auth layer.

Path validation against the configured roots still applies - that is input
validation, not authorisation, and it prevents a malformed request from touching
files outside the library.

On first run with an empty database, start with sensible defaults, no Plex, no *arr
instances, no roots. The UI walks through adding them. Ingest does nothing until at
least one root exists.

---

## 22. Docker

Multi-stage build producing one image with everything needed. No compose file or
Kubernetes manifests.

```dockerfile
# --- frontend ---
FROM node:22-alpine AS web
WORKDIR /src
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build          # -> /src/dist

# --- backend ---
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /codarr ./cmd/codarr

# --- runtime ---
FROM debian:bookworm-slim
# intel-media-va-driver-non-free lives in Debian's non-free component, which the
# stock image does not enable. Without this sed the build fails at apt-get.
RUN sed -i 's/Components: main/Components: main contrib non-free non-free-firmware/' \
      /etc/apt/sources.list.d/debian.sources \
 && apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata gnupg curl \
 && curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key \
      | gpg --dearmor -o /usr/share/keyrings/jellyfin.gpg \
 && echo "deb [signed-by=/usr/share/keyrings/jellyfin.gpg] \
      https://repo.jellyfin.org/debian bookworm main" \
      > /etc/apt/sources.list.d/jellyfin.list \
 && apt-get update && apt-get install -y --no-install-recommends \
      jellyfin-ffmpeg7 \
      intel-media-va-driver-non-free \
      libva-drm2 libva2 vainfo \
 && apt-get purge -y gnupg curl && apt-get autoremove -y \
 && rm -rf /var/lib/apt/lists/*

ENV PATH="/usr/lib/jellyfin-ffmpeg:${PATH}"
COPY --from=build /codarr /usr/local/bin/codarr
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/codarr"]
```

- **jellyfin-ffmpeg** rather than distro ffmpeg. It is patched for this workload,
  bundles a working Intel driver stack, and handles HDR metadata better than stock
  builds. Re-check the package name and repository path at build time; Jellyfin has
  renamed these between releases.
- `intel-media-va-driver-non-free` is required for QSV on Gen 9.5 - the free driver
  will not do HEVC encode. jellyfin-ffmpeg bundles its own driver copy as well;
  `vainfo` shows which one actually loaded.
- The container needs `/dev/dri` passed through, and the process needs the host's
  `render` group GID as a supplementary group.
- Build `linux/amd64` as the real target.
- Verify hardware access inside the container with `vainfo` before debugging
  anything else.

---

## 23. Deployment tasks outside the application

These are changes to the surrounding infrastructure, not to Codarr. They are part
of shipping it. Apply them via GitOps and `kubectl`, and record them in the README
so the deployment is reproducible.

### 23.1 Plex client profile for HEVC in browsers

**Without this, HEVC files transcode for browser clients regardless of what the
browser can decode.** Plex Media Server decides from its own client profile, not
from browser capability. Since HEVC is now the only encode target, this profile is
what makes browser playback Direct Stream instead of Transcode.

Create `Chrome.xml` in the `Profiles` directory of the Plex data directory - the
directory does not exist by default and must be created. Files there override the
defaults in the installation directory and survive Plex updates.

```xml
<?xml version="1.0" encoding="utf-8"?>
<Client name="Chrome">
  <TranscodeTargets>
    <VideoProfile protocol="hls" container="mpegts" codec="hevc,h264" audioCodec="aac,mp3" context="streaming" />
    <VideoProfile protocol="dash" container="mp4" codec="hevc,h264" audioCodec="aac" context="streaming">
      <Setting name="ForceTranscodesForLive" value="true" />
      <Setting name="SkipAudioBeforeStart" value="true" />
    </VideoProfile>
    <VideoProfile protocol="http" container="mkv" codec="hevc,h264" audioCodec="aac,mp3" context="streaming" />
    <MusicProfile container="mp3" codec="mp3" />
    <PhotoProfile container="jpeg" />
    <SubtitleProfile container="ass" codec="ass" context="all" />
  </TranscodeTargets>
  <CodecProfiles>
    <VideoCodec name="*">
      <Limitations>
        <UpperBound name="video.bitDepth" value="10" />
      </Limitations>
    </VideoCodec>
    <VideoAudioCodec name="*">
      <Limitations>
        <UpperBound name="audio.channels" value="6" />
      </Limitations>
    </VideoAudioCodec>
  </CodecProfiles>
  <TranscodeTargetProfiles>
    <VideoTranscodeTarget protocol="*" context="streaming">
      <VideoAudioCodec name="*">
        <Limitations>
          <UpperBound name="audio.channels" value="2" onlyTranscodes="true" />
        </Limitations>
      </VideoAudioCodec>
    </VideoTranscodeTarget>
  </TranscodeTargetProfiles>
</Client>
```

**`video.bitDepth` is set to 10, not the 8 that the commonly circulated version of
this profile uses.** With 8, every HEVC Main 10 file - which is every HDR file in
the library, all of them copied untouched by this policy - would still transcode
for browsers, defeating the point of installing the profile at all. If 10-bit
playback misbehaves in a particular browser, lower it to 8 deliberately and accept
that HDR transcodes there.

`audio.channels` at 6 matches the AC3 5.1 conversion target; the
`TranscodeTargetProfiles` block downmixes to stereo only when a transcode is
happening anyway.

Deployment: mount the file into the Plex pod's data directory at
`{data_dir}/Profiles/Chrome.xml`, via a ConfigMap or the existing config volume,
and **restart the Plex pod** - the profile is read at startup.

Verify by playing an HEVC file in Chrome and confirming the Plex dashboard reports
Direct Stream rather than Transcode.

### 23.2 *arr configuration

**Upgrades are disabled on every Radarr and Sonarr instance.** This is what closes
the re-grab risk in 16.2 and is a precondition for `full` jobs being safe. Confirm
it stays that way; if upgrades are ever re-enabled, turn on `unmonitor_after` for
the affected instances first.

**Renaming must stay off**, or the naming format must contain no `{MediaInfo ...}`
tokens. Codarr never renames, but an *arr rename pass triggered later would churn
paths for files whose codec info has changed, for no benefit.

### 23.3 Cluster

- Pass `/dev/dri` into the Codarr pod - Intel GPU device plugin
  (`gpu.intel.com/i915`) or a hostPath mount plus
  `securityContext.supplementalGroups` with the host's `render` GID.
- Pin to the amd64 node with the iGPU via nodeSelector; arm64 nodes have no Quick
  Sync and would silently fall back to software encoding.
- The Plex pod and the Codarr pod share that iGPU. This is intended (16.1).

---

## 24. Observability

Prometheus at `/metrics`:

```
codarr_jobs_total{state,kind,origin}
codarr_queue_depth
codarr_transcode_duration_seconds{kind,encoder}   histogram
codarr_transcode_estimate_error_seconds           histogram
codarr_bytes_in_total
codarr_bytes_out_total
codarr_bytes_saved_total
codarr_encoder_fallback_total{from,to}
codarr_decode_fallback_total
codarr_files_by_plan_kind{kind}
codarr_plex_up
codarr_plex_active_sessions
codarr_arr_up{instance}
codarr_jobs_awaiting_stream_end
codarr_jobs_failed_total{failure_code}
codarr_jobs_requeued_total
codarr_errors_total{category}
```

**Logging goes to stdout.** Use `log/slog` with a JSON handler writing to stdout,
so container logs work normally with `kubectl logs` and whatever collection sits
behind it. Include the job id on every job-related line.

Additionally, wrap the handler so records at info level and above are also written
to the `events` table for the UI's log view. Stdout is the source of truth; the
table is a convenience for the UI and should be pruned on a schedule (30 days or
100k rows, whichever is smaller). **Never let a database write failure prevent the
stdout log line from being emitted.**

Secrets: never log the Plex token or *arr API keys, and never return them from
`GET` - return a masked placeholder.

---

## 25. Build order

Each phase ships with its tests. A phase is not done until they pass.

**Phase 0 - foundations.** Repo layout, `api/openapi.yaml` skeleton,
`oapi-codegen` wiring, moq wiring, migrations, store with its concurrency test, CI
running `go test ./...` and `golangci-lint`.

**Phase 1 - read only.** ffprobe wrapper, decision engine, transform record
generation, dry run over a directory. Library table and detail modal.

At the end of phase 1 the tool answers "what in my library needs work, and why"
without writing a byte. The decision engine test suite is the deliverable as much
as the code.

**Phase 2 - config and connections.** Settings, Plex, multiple *arr instances,
path mappings, root import, Test buttons, attribution by longest-prefix match.

**Phase 3 - audio, subtitles and remux.** `audio_only` and `remux` with
`-c:v copy`. No hardware needed. Verification, duration estimation. **Write to a
scratch directory, not over the source.** This fixes most of the library at low
risk because video is never touched.

**Phase 4 - in-place replacement.** Destination-side staging, fsync, atomic rename,
permission and mtime preservation, nlink and device checks, output identity
recording and provenance derivation (section 12), crash recovery, preflight.

**Phase 5 - video encoding.** Hardware probe including decode, bitrate sample
probe, `full` plans on both decode paths, HDR, Dolby Vision detection, level
rewrite, deinterlacing.

**Phase 6 - ingest.** Webhooks per instance including delete events, scheduled scan
with the stability window and prune, exclusions, ignore list.

**Phase 7 - Plex actions.** Partial scan, analyze, active stream guard, deferred
publish. Install and verify the client profile from 23.1.

**Phase 8 - polish.** Metrics, bulk re-check, space sweep, `unmonitor_after`,
Docker image.

Phases 3 and 5 are deliberately split: audio work is safe and covers most of the
value; video encoding is where the risk lives.

---

## 26. Checklist

- [ ] Never change resolution
- [ ] Never change audio channel count except 7.1+ downmix on conversion
- [ ] Never produce a file with zero audio streams
- [ ] Never re-encode video that passes the copy test
- [ ] Never change the container for MKV or MP4 sources - the filename must not
      change
- [ ] Never write `srt` into an MP4 - use `mov_text`
- [ ] Never write AC3 into an MP4 - use multichannel AAC
- [ ] Never mistake an attached picture stream for the video stream
- [ ] Never tonemap HDR - encode Main 10 instead
- [ ] Never re-encode Dolby Vision profile 5 video
- [ ] Always verify the DOVI configuration record survived for profile 5
- [ ] Always test chroma as subsampling, never as a `pix_fmt` string compare
- [ ] Always treat unknown or absent `field_order` as progressive
- [ ] Always rewrite, never re-encode, level-only H.264 failures that fit 4.2 with
      `refs <= 4`
- [ ] Always index codec, disposition and bitstream-filter options by OUTPUT stream
      position
- [ ] Always `-map` every kept stream explicitly
- [ ] Always preserve chapters, language tags and dispositions
- [ ] Always tag output to prevent re-processing loops
- [ ] Never skip a file on the CODARR tag alone - the output fingerprint must
      match too, or a third-party rewrite becomes invisible forever
- [ ] Always record the output fingerprint at promotion, after restoring mtime
- [ ] Never whole-file hash during a scan - the sparse fingerprint is the drift
      check, the full hash is an on-demand audit
- [ ] Always use `-nostdin`
- [ ] Never pass `-hwaccel` for a source codec outside the hardware-decode set
- [ ] Never put `-pix_fmt` on a hardware-surface pipeline - formats go in the
      filter chain
- [ ] Never put `bwdif` on a hardware-decode pipeline - use `vpp_qsv=deinterlace`
- [ ] Always add `-fflags +genpts` on legacy-container inputs
- [ ] Always tag HEVC in MP4 as `hvc1`
- [ ] Always write MP4 output with `-movflags +faststart+use_metadata_tags`
- [ ] Never create a second active job for the same file - enqueue is idempotent
- [ ] Always verify before promoting
- [ ] Always stage on the destination filesystem when space allows
- [ ] Always re-check Plex sessions immediately before the rename
- [ ] Always re-queue interrupted jobs automatically, wiping their staging, capped
      at 3 automatic attempts
- [ ] Always fail on `nlink > 1`
- [ ] Always show software-encoder fallback loudly
- [ ] Always write the transform record at enqueue and update it at completion
- [ ] Always attribute files to an *arr instance by longest-prefix match
- [ ] Never delete the source before the output is verified and promoted
- [ ] Never wipe Plex metadata - analyze instead
- [ ] Never start on a file still being written
- [ ] Never log or return secrets
- [ ] Never build an auth layer - access is secured externally
- [ ] Never build SSE or WebSockets - the UI polls every 10 seconds
- [ ] Never pause the queue because Plex is streaming
- [ ] Never drop a standalone text subtitle (subrip, ass, ssa, webvtt, mov_text)
      for format reasons - convert it; teletext and EIA-608 are dropped
- [ ] Never leave a `failed` job without a `failure_code` and a readable message
- [ ] Never add a trash, undo or restore path - promotion is irreversible
- [ ] Never add a "force promote despite failed verification" escape hatch
- [ ] Always dry-run and confirm anything touching more than one file, and say
      plainly in the confirmation that it cannot be undone
- [ ] Never treat a `chown` failure as a job failure - `root_squash` is expected
- [ ] Always persist job progress to the database, throttled to every 5 seconds
- [ ] Always log to stdout as JSON, with the events table as a secondary sink
- [ ] Never run the space sweep automatically

---

## 27. Verify against live systems

1. HDR10 metadata survives a `hevc_qsv` round trip on this ffmpeg build. Transcode
   one HDR file, ffprobe the output, confirm `side_data_list` still carries
   mastering display and MaxCLL/MaxFALL.
2. The Plex `analyze` endpoint verb and path on the running PMS version.
3. The ffprobe JSON path for the Dolby Vision profile number, and that the DOVI
   configuration record survives `-c:v copy` into both MKV and MP4 on this build -
   the profile 5 gate depends on it.
4. That the Chrome client profile (23.1) actually produces Direct Stream for both
   8-bit and 10-bit HEVC in the browsers in use. Check the Plex dashboard, not the
   browser.
5. That *arr renaming is off on all four instances, or the naming format uses no
   `{MediaInfo ...}` tokens.
6. The current jellyfin-ffmpeg Debian package name and repository path.
7. That `vainfo` inside the container reports the expected encode profiles, and
   which driver loaded - the bundled jellyfin one or the distro iHD.
8. That the 7.1 to 5.1 downmix produces a sensible channel layout rather than
   whatever ffmpeg picks by default.
9. That ASS to SRT conversion produces readable output on a sample file, since
   heavily typeset sources will lose positioning.
10. That `rename()` over an existing file on the NFS mount behaves atomically and
    that a dotfile sibling in the destination directory reports the same device
    number as its target.
11. Whether `chown` succeeds on the NFS mount, or whether the export uses
    `root_squash`. Ownership restore is best-effort either way.
12. That the CODARR loop-prevention tag survives a round trip into MP4 via
    `-movflags use_metadata_tags` alongside `+faststart`, and reads back through
    ffprobe. If not, fall back to the `comment` field. Confirm before enabling MP4
    output.
13. VMAF spot-check two or three `hevc_qsv` encodes against their probe targets;
    tune the 1.35 correction constant.
14. That VP9 hardware decode works on this Gen 9.5 driver stack; if not, move `vp9`
    to the software-decode set.
15. That a level-rewritten file (`h264_metadata=level=4.2`) plays on the pickiest
    client, ideally the oldest TV.
16. Whether Bazarr (or whatever else touches the library) preserves the CODARR
    global tag when it rewrites a file. If it does, the fingerprint conjunction in
    section 12 is the only thing preventing those files from being skipped
    forever - confirm the detection fires on a deliberately modified file.
