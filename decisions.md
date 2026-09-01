# Decisions

Judgement calls made while implementing `plan.md`, for review. Anything `plan.md`
states outright is not here; this is only where it was silent, or where it
disagreed with the conventions of the sibling projects in `../bolan/`.

Format: what was decided, what the alternative was, why.

## Structure and tooling

### Repo layout follows bolan-api, not the plan's implied layout

`plan.md` names packages but not where they live. Adopted bolan-api's shape:
shared code under `internal/pkg/` rather than a root `pkg/`, migrations in
`data/migrations` beside `data/embed.go`, one binary under `cmd/codarr` rather
than several.

Alternative: the flat `bolan-self-crawler` layout. Rejected, this project is
far past the size where that stops helping.

### The Dockerfile lives at `.build/Dockerfile`

`plan.md` section 22 gives the Dockerfile content but not its path.
`build-push.yaml` in both bolan repos passes `file: .build/Dockerfile`, so
keeping the path identical means the workflow copies across unchanged.

### `data/migrations/001_schema.sql` is transcribed verbatim from plan.md 17.1

Validated by executing it against an in-memory SQLite: 11 tables, 8 indexes,
`foreign_keys=ON`. Following bolan's rule that the consolidated starting schema
is never edited after landing; corrections ship as new numbered files.

### Logging is `log/slog`, not zap

`plan.md` section 24 says `log/slog` with a JSON handler explicitly. bolan-api
uses `go.uber.org/zap`. The plan wins, and it happens to make the events-table
sink cheaper: it is a plain `slog.Handler` wrapper rather than a custom
`zapcore.Core`.

Consequence: `sloglint` in `.golangci.yaml` becomes meaningful rather than
inert, and `go.uber.org/zap/zapcore.Core` came out of the `ireturn` allow list.

### Migrations use `rubenv/sql-migrate` on SQLite

bolan-api's choice, and it works unchanged against SQLite. `plan.md` says
"embedded migrations" without naming a library.

### `internal/pkg/domain`, `clock` and `fsx` were written before any work package

`plan.md` section 2.2 asks for narrow interfaces at every boundary and lists
`Clock` and `FS` among them. They are defined up front because every later
package depends on them, and because parallel work on separate packages needs a
fixed shared type contract to avoid colliding.

`domain.DeriveProvenance` exists so the rule in section 17.1 has exactly one
definition that the store, the analyzer and the UI all call.

## Frontend

### Tailwind v4 as a Vite plugin, not the CDN script bolan-admin-fe uses

bolan-admin-fe loads `cdn.tailwindcss.com` from a script tag and inlines its
config in `index.html`. Three reasons that does not transfer: the CDN build
compiles CSS in the browser at runtime and ships roughly 400KB of JS, it is
deprecated in Tailwind v4, and it makes a self-hosted tool depend on a third
party being up. The whole point of `go:embed` is that the binary is the
deployment.

Confirmed with the user before adopting.

### `openapi-typescript` plus `openapi-fetch`, not `@hey-api/openapi-ts`

`plan.md` section 2.1 names both packages explicitly and calls the shared spec
the main payoff of working spec-first. bolan-admin-fe generates types only and
hand-writes the client, which its own `renovate.json5` flags as a drift risk CI
cannot see. CI here regenerates and runs `git diff --exit-code`, closing that.

### `BrowserRouter`, and a same-origin relative API base

bolan-admin-fe uses `HashRouter` and an absolute `API_BASE_URL` because it ships
as a separate nginx container talking to a different host. Here the Go binary
serves both the SPA and the API from one origin, so neither is needed. Vite's
dev server proxies `/api` to the Go server.

### Vite writes to `../internal/web/dist`

Rather than `web/dist` plus a copy step. `go:embed` needs the output inside the
module, and this way `make build` works without Docker.

`internal/web/dist/.gitkeep` is committed and everything else in that directory
is gitignored, so `//go:embed all:dist` resolves on a clean checkout. The
handler reports plainly when `index.html` is absent instead of failing to build.

### `strict: true` in tsconfig

bolan-admin-fe omits it. Reading the repo, that looks like an oversight from its
AI Studio export rather than a decision.

### Not adopted from bolan-admin-fe

The duplicate `constants.ts` / `constants/api.ts` pair with contradictory values,
the `@` path alias nothing imports, the leftover `process.env.GEMINI_API_KEY`
define, and the `lint:raw-colors` script that is defined but never run.

Kept, because it is load-bearing there: the singleton `ToastManager` the API
client fires directly on failure, so components only handle the success path.

## Build and CI

### Image is `linux/amd64` only

bolan builds `linux/amd64,linux/arm64`. QSV, `intel-media-va-driver-non-free`
and the whole hardware path are amd64. `plan.md` 23.3 also notes that the arm64
nodes would silently fall back to software encoding, which is the exact failure
this project exists to avoid.

Consequence for the cluster: `ghcr.io/yama6a/codarr` needs adding to
`SKIP_IMAGES` in `offgrid-private`'s `lib/shell/check_multiarch.sh`, after the
amd64 pin is in the chart, not before. Noted in the README.

### Runtime base is `debian:bookworm-slim`, not distroless

bolan runs `gcr.io/distroless/static:nonroot`. Codarr needs jellyfin-ffmpeg7 and
the Intel VAAPI driver stack, neither of which exists in distroless.

### The frontend builds inside the Dockerfile

bolan builds `dist/` on the GitHub runner and the image only copies it, so one
build serves both architectures. Here `go:embed` needs `dist` present at Go
build time, and there is only one architecture, so the node stage moves into the
Dockerfile.

### No `deploy` job in `build-push.yaml`

bolan's build-push clones `offgrid-private`, seds the image tag and opens a PR.
The user chose to keep Kubernetes out of this repo, so the workflow stops after
the release and the README documents the wiring to paste by hand.

### CI adds two drift checks bolan lacks

`go generate ./... && git diff --exit-code` and the same for the TypeScript
types. bolan's own renovate config comments that generated-code drift is
invisible to its CI; this closes that rather than reproducing it.

### Health endpoints exist twice

`plan.md` section 20 specifies `/api/health` and `/api/ready`. bolan serves
`/healthz` and `/readyz` on a plain mux **above** the chi router, so kubelet
probes skip CORS, logging and auth middleware. Both are served: the `/api` pair
because the spec says so, the bare pair because that is what the cluster's
probes should point at.

## Wave 1

### Generated API code lives in `api/`, not `internal/api/generated.go`

`plan.md` 2.1 shows `output: internal/api/generated.go`. Everything else in that
section, and bolan-api's layout, puts the spec and its generated output together
in `api/` and the hand-written handlers in `internal/api/`. Went with `api/`, in
two files (`models.gen.go`, `server.gen.go`) as bolan does. The `oapi-codegen.yaml`
snippet in 2.1 is now stale.

### `//go:generate go run github.com/oapi-codegen/...` rather than a bare binary

Pinned through `tools.go`, so `make generate` and CI need no separate install
step. This is the form `plan.md` 2.1 shows.

### `always-prefix-enum-values: true` on both codegen configs

Without it oapi-codegen only prefixes enum constants when two enums collide, so
the `MediaSort` values `plan_kind` and `provenance` emitted bare `PlanKind` and
`Provenance` constants that collided with the type names of the same spelling.

### `recheck-selected` takes a filter as well as ids

`plan.md` 18.2 asks for "select all matching filter" in the library table but
section 20 gives no filter-based bulk endpoint, so selecting a 40k-file filter
would have meant shipping 40k integers. `RecheckSelectedRequest` now accepts
either `ids` or a `MediaFilter` mirroring the library table's own filters. An
empty body selects nothing rather than everything.

### Errors are one `default` response, not enumerated status codes

`{error, message, details}` on every operation. Enumerating 400/404/409 per
operation multiplies the generated response types by four for no gain.

### The Plex PIN poll never returns the token

`{authorized, token_stored}` only. Returning it would contradict "never return
secrets" on the one path where a secret is legitimately in flight.

### Timestamps are fixed-width RFC 3339 UTC strings

Nine fractional digits, always. `time.RFC3339Nano` strips trailing zeros, which
breaks `ORDER BY queued_at` in `ClaimNextJob`: the stored text has to sort like
the instant it represents. `mtime` columns stay unix seconds, which is what
`os.FileInfo` gives and what the next scan compares against.

### The read pool carries `_query_only=1`

`plan.md` 17 asks for a single write connection alongside a read pool. Marking
the read pool query-only makes "every write goes through the write pool" a
property SQLite enforces rather than a convention the next contributor has to
know about.

### The interrupted-job sweep is three-way, and never guesses

`running` and `verifying` are requeued or failed in the store. `promoting` and
`awaiting_stream_end` come back as "needs check" and are **left in the state
they were found in**, because deciding them needs the filesystem: whether the
`rename()` landed (19.2's CODARR-tag check) or whether the staging file still
verifies. Leaving them in place also keeps the partial unique index holding, so
the worker cannot claim them mid-decision.

### Migration 002 adds the throughput_stats natural key

`plan.md` 17.1 ships no unique constraint on `(kind, encoder, resolution)`,
which is plainly that table's key given 14.3. Without it an upsert is
UPDATE-then-INSERT, correct only because there is one write connection. The
index makes it correct regardless. `COALESCE` on the two nullable columns,
since NULLs never compare equal in a unique index.

### `Store` is one wide interface, but consumers define narrow ones

`plan.md` 2.2's table lists `Store | all DB access` as a single boundary, and
that is what `internal/pkg/store` exposes: 74 methods. The same section also
says interfaces should be small and defined by the consumer. Both hold, at
different layers: the store package publishes the wide interface, and each
consumer declares the three or four methods it actually uses, satisfied by the
same concrete value. Tests mock the narrow one.

### Icons are inline SVG via `lucide-react`, not an icon font

The first cut pulled `material-symbols`, whose woff2 is 3.8 MB. `go:embed` puts
the whole `dist` inside the binary, so a handful of icons cost 3.8 MB of
executable. `lucide-react` tree-shakes to the ~20 icons actually imported.
`internal/web/dist` went from 4.4 MB to 360 KB.

Inter is pinned to the latin subset by a hand-written `@font-face` for the same
reason: fontsource's stylesheet also ships cyrillic, greek and vietnamese, none
of which this UI renders.

### `web/go.mod` exists so `./...` stops at `web/`

npm packages occasionally ship Go source (`flatted` does), and Go's `...`
pattern does not skip `node_modules`. On a fresh CI checkout `go test ./...`
would try to build a dependency's vendored Go. A nested module is the
documented way to cut a subtree out of the parent module. There is no Go code
under `web/`.

### CI checks formatting drift, not just generated-code drift

`make ci` runs `gofumpt -w .`, which rewrites rather than reports. Without a
following `git diff --exit-code` the runner silently fixes formatting and the
branch stays unformatted forever.

## *arr configuration

### Renaming stays on, with its existing MediaInfo tokens

All four instances rename on import using formats containing
`{Mediainfo VideoCodec}`, `{Mediainfo AudioCodec}`, `{Mediainfo AudioChannels}`
and `{MediaInfo VideoBitDepth}`. `plan.md` 23.2 asks for renaming off, or those
tokens removed.

Reviewed with the user and left as it is. Codarr never renames, so nothing it
does moves a path. What the tokens cost is that after a `full` job the filename
carries stale codec info, and a *manual* rename pass would then move the file.
Neither Radarr nor Sonarr renames existing files on a rescan, so this does not
fire on its own.

If a rename pass does run, the cost is bounded: the path changes, Plex sees one
part removed and another added, and Codarr's row for the old path goes `missing`
while the new path comes in fresh and loses its provenance link. It does **not**
cause a re-encode loop, because the decision engine independently plans an
already-compliant file as `skip`. The CODARR tag is an optimisation; the real
protection is that a compliant file plans to nothing.

Upgrades are off on every instance, which is the load-bearing half of 23.2 and
is confirmed. See `VERIFY.md`.

## Wave 2

### HLG is stamped as HLG, not as PQ

`plan.md` 9 defines a stream as HDR when `color_transfer` is `smpte2084` (PQ)
**or** `arib-std-b67` (HLG), then prescribes `-color_trc smpte2084`
unconditionally for an HDR encode. Those contradict: an HLG source re-encoded
with a PQ transfer flag renders visibly wrong.

`domain.Plan` now carries `HDRTransfer` and the argv builder emits it, empty
meaning PQ. Deviation from the letter of section 9, in favour of its intent.
Rare in practice, since HDR sources are almost always HEVC Main 10 and get
copied, but silent and visible when it fires.

### `-bsf:v` rather than `-bsf:v:0` for the level rewrite

`plan.md` 14.2 lists `-bsf:v:N` among the output-indexed options; 14.1's worked
example writes `-bsf:v`. Followed 14.1, which is internally consistent: every
video option uses the bare `:v` spec (`-c:v`, `-b:v`, `-profile:v`, `-tag:v`)
because there is exactly one video output stream. Only audio and subtitles carry
indices, plus dispositions and per-stream metadata, whose specs require one.

### `-y` is on every invocation

Not in the plan's argv listings. A staging file can survive a crash, and with
`-nostdin` the overwrite prompt becomes a hard failure rather than a hang.

### Resolution tiering is by width, not height

For both the BPP table (8.2) and the floors and ceilings (8.3). Letterboxing
shrinks height, not width, so tiering by height drops a 1280x536 scope transfer
into the 576p tier, whose 2.5 Mbps ceiling would visibly wreck it.

### The 1.35 hardware correction applies to the sample probe only

`plan.md` 8.1 applies it to the probe result. 8.2's fallback table is labelled
"BPP (HEVC)", so it is already a target for this encoder rather than an x265
measurement; correcting it too would double-count.

### The 8.1 clamps also apply to the 8.2 fallback

8.2 does not say to. Without it the formula returns 12.9 Mbps for 1080p60,
well past the 8 Mbps ceiling in 8.3.

### Sample segments are pulled back to fit short sources

8.1 assumes a feature-length file. On a 45-minute episode the 80% window runs
past EOF and the measured bitrate is wrong by the truncation ratio. Windows are
clamped inside the source, duplicates collapsing on the same start are dropped,
and anything under 60 seconds falls back to a single whole-file sample.

### The 0.85 source clamp is skipped when the source bitrate is unresolved

Rung 5 of 8.4 leaves it at zero, and clamping against zero would floor every
target to nothing.

### Stream indices are per-type ordinals, not absolute

`StreamPlan.SourceIndex` and `OutputIndex` count within a stream type, so
subtitle 1 is `0:s:1`. This matches 17.2's own example, which numbers subtitles
0, 1, 2 in a file whose global indices would be 3, 4, 5, and it matches the
`-map 0:s:1` form throughout 14.1. It is the one assumption shared across
`decide` and `ffmpeg` that would silently produce a wrong file if the two
disagreed, so it is stated here rather than left implicit.

### `Container.OutputExt` takes the source path

The first cut was `Ext()` returning `.mp4` for the MP4 family, which renames an
`.m4v` source and breaks 6.1's "the filename never changes". The extension now
comes from the source path when the container family is unchanged, preserving
case, and only a legacy container (which is becoming MKV anyway) gets a new one.

### The 8.4 chain lives in `decide` only

It was briefly implemented in both `decide` and `ffmpeg`: same rung order, same
2% container allowance, two test suites to keep in step. `decide` keeps it,
because the transform record's `before` needs it and it already depends on
ffprobe. The encoder takes the resolved number as a parameter.

### Dropping attachments or cover art does not by itself force a rewrite

6.4 says "drop all attachments" and 6.2 says always drop attached pictures, but
7's Kind table says a file whose streams all copy is `skip`. Read literally,
every anime MKV with font attachments and every MP4 with cover art becomes
`audio_only` forever, purely to strip them, and gets rewritten on every pass.

Treated as mapping rules for a file already being rebuilt for a real reason.
This is the judgement call in wave 2 with the least support in the text.

### A level rewrite forces at least `remux`

7's Kind table calls an all-copy MKV `skip`, which would mean a level-5.1 1080p
file is skipped and the flag is never actually rewritten. 6.2 says the kind
stays "remux or audio_only". Followed 6.2. Never `full`.

### Dolby Vision profile 5 does not force `audio_only`

9 says detecting it "downgrades the plan to `audio_only`", but if nothing else
needs work there is no audio work to do and the honest kind is `skip`.
Implemented as "never `full`", with the kind falling out of the other streams.

### Default deny extended to two cases the spec leaves open

An h264 stream with no reported level encodes, and the level-rewrite guard
requires `1 <= refs <= 4`, so an absent `refs` is a rejection rather than a
pass. Both follow 6.2's stated default-deny posture.

### The hardware-decode set is not in the policy hash

It selects a decode path, not what ends up in the output. Including it would
make a driver-support change invalidate the whole library on the next
"re-check all done items".

### Promotion defers on a Plex error rather than failing

15.2 does not say what to do when Plex cannot be reached. Failing closed was the
first cut, but `awaiting_stream_end` is equally closed: it never replaces the
file, it keeps the verified staging output, it retries every 60 seconds, and
19.2 makes it resumable across a restart. Failing throws away a completed encode
and needs a human. Only an unambiguous "not streaming" reaches the rename.

### The same-device check is made load-bearing rather than tautological

15.4 asks that the staging and destination directories report the same device,
but 15.1 makes staging a sibling of the destination, so on the primary path they
are the same directory and the comparison can never fail. `Staging.CrossDevice`
carries the real signal: a hard preflight failure on the non-temp path, and on
the temp path it decides whether the copy is needed at all. Kept as 15.6's
tripwire, because NFSv4 hides a dataset split behind identical client-side paths
while `rename()` silently starts returning EXDEV.

### A free-space re-check runs before the cross-device copy

15.1 falls back to the temp dir when the destination lacks space, then requires
copying the output back to a destination-side staging file. Nothing in the spec
checks that the output fits before that copy starts, and a mid-copy ENOSPC
leaves a partial dotfile only the sweep cleans up. The output is stat'd and the
destination statfs'd first.

### A post-rename failure still surrenders the output identity

If `chmod`, `chtimes` or the fingerprint fails after the rename, the file is
promoted but the job failed. `Result.Renamed` tells the caller the source is
already gone and that it must persist whatever `Identity` was computed. Without
it, `codarr_output_fingerprint` stays NULL on a file Codarr wrote and provenance
reads `untouched` forever. Not a safety problem, since the decision engine plans
a compliant file as `skip` regardless, but wrong in the UI.

## Wave 3

### 13.1's list of webhook fields is wrong for Rename events

`plan.md` 13.1 says the fields read are `movieFile.{id,relativePath,path}` and
`episodeFile.{...}`. A **Rename** payload carries neither. Radarr sends
`renamedMovieFiles[]` and Sonarr `renamedEpisodeFiles[]`, each entry adding
`previousPath` and `previousRelativePath`.

A parser following 13.1 literally would read a rename as an event with zero
files and leave the stored path pointing at a file that no longer exists. The
parser handles both shapes.

This matters more here than it would elsewhere: renaming is on with MediaInfo
tokens on all four instances (see `VERIFY.md`), so Rename is the event most
likely to fire after a `full` job changes a file's codec.

### The Plex rating-key lookup happens before the refresh, not after

16.1 lists the partial refresh first and `analyze` second. The refresh is
asynchronous, so a rating-key lookup immediately after it can race the scan and
miss. The lookup runs first, which normally works because Codarr keeps the path
stable (16.2), with a second lookup after the refresh for the one case where the
path does change: a legacy container becoming MKV (6.1).

### The session listing is never cached, only the rating-key resolution

16.1 says to "cache briefly" when checking for active streams. Caching the
`/status/sessions` listing would be wrong: `promote` re-checks immediately before
the rename specifically to close 15.6's ESTALE window, and a cached answer there
reopens the race the check exists to close.

What is cached is the `ratingKey` to `Media[].Part[].file` resolution, 30 second
TTL, kept short because a `full` job can change a file's extension. Same call
volume in steady state, no correctness hole.

### Radarr's unmonitor PUT round-trips the whole movie as raw JSON

16.2's `PUT /api/v3/movie` replaces the entire resource. Unmarshalling into a
typed struct and marshalling it back would blank every field Radarr has added
since this code was written, so the movie is carried as
`map[string]json.RawMessage` with only `monitored` altered.

### The scan refuses to prune when a root does not stat as a directory

13.2 says any `media_files` row whose path no longer exists is marked `missing`.
Taken literally that is dangerous here: an unmounted or unreachable NFS export
stats as an error and walks as an empty tree, so a single bad mount would retire
the entire library in one pass and wreck the dashboard's compatibility summary.

Two guards, neither in the plan: a root that does not stat as a directory is
skipped whole with no pruning at all, and every prune candidate is stat-confirmed
individually before being marked missing. The `softerr` mount option confirmed in
`VERIFY.md` makes the unreachable-export case realistic rather than theoretical.

### The hardware probe serves the cache at startup and only forces on demand

10.1 says to probe "at startup and on demand" but also to cache in SQLite and
re-probe when the ffmpeg version changes. Taken literally every restart burns six
ffmpeg invocations for an answer already stored. Startup serves the cache when
every row carries the current ffmpeg version; the UI button forces a fresh probe.

### Migration 003 adds a device column to hw_capabilities

17.1's `hw_capabilities` is keyed on the ffmpeg version alone, so changing
`qsv_device` in the UI leaves a stale answer about a device no longer in use.
The probe now records the device it ran against.

### The decode retry runs before the encoder chain

10.1 gives one software-decode retry and 10.2 gives a three-step encoder chain,
with no statement of which runs first or how they share an attempt budget.

Decode retry first, once, on the same encoder: it is far cheaper than dropping to
libx265, and 6.2's software-decode sources go through dav1d comfortably faster
than realtime. Then step the encoder chain, resetting the decode-retry flag at
each step because a different backend fails differently.

ffmpeg's stderr is never parsed to classify the failure. Driver error strings are
not a stable API, so the cheap retry is simply tried before the expensive one.

These are in-job retries and are deliberately NOT part of `domain.MaxAutoAttempts`,
which counts process-death interruptions (19.2).

### `scan_cron` is five numeric fields

13.2 names the column but no dialect. Implemented in-package as five fields with
`*`, ranges, lists and steps. No `@daily`, no `MON`. A richer dialect would mean
taking a cron dependency, which is not worth it for one scheduled sweep.

### A webhook Test succeeds even for an instance disabled in Codarr

The operator is pasting the URL and clicking Test at that moment; answering 500
because the instance is not enabled yet would be actively unhelpful. Non-Test
events for a disabled instance are acknowledged and ignored.

### The `promoting` consistency check tests the fingerprint before the tag

19.2 says to detect whether the rename landed "by checking the destination file
for the CODARR tag with the current policy hash". That is not sufficient on its
own, for exactly the reason section 12 gives: mkvmerge preserves global tags, so
a file promoted by an earlier run and modified since carries a valid tag and a
matching policy hash.

The sparse fingerprint is checked first, against what analysis recorded, which
proves the file is still the source rather than an output. The tag is checked
second. The case 19.2 says cannot happen (neither matches) fails with
`promote_failed` naming both, rather than guessing.

The same check now also covers `awaiting_stream_end` when the staging file has
gone, instead of 19.2's literal "delete and re-queue".

### Migration 004 persists ffmpeg's final out_time

15.3's legacy-container fallback compares the output against ffmpeg's own
`out_time` rather than the source's header, because VOB and AVI headers lie. 14.3
keeps that value in memory, but 19.2 resumes `awaiting_stream_end` across a
process restart, at which point the in-memory value is gone and a resumed job on
a legacy container has no fallback. Now a column.

### A failed bitrate sample probe falls back rather than failing the job

19.1 lists `probe_failed` for "ffprobe or the bitrate sample probe errored", but
8.2 exists precisely to give the sample probe a fallback. Followed 8.2: warn and
use the formula. `probe_failed` is reserved for a real ffprobe failure, where
there is genuinely nothing to go on.

### Section 11's 35% threshold is applied to file size, not video bitrate

The table in 11 quotes video-bitrate ratios but the confirmation shows a total
projected saving in bytes. Applying the threshold to the projected file size is
strictly more conservative, since audio and subtitles do not shrink. Sweep
candidates carry both numbers so the UI can show either.

### The worker re-plans from the stored probe rather than trusting `jobs.kind`

It is the only way to surface `NeedsIdetSample`, and it keeps the plan that
actually executes consistent with the policy in force at run time rather than at
enqueue time. A job whose plan has become `skip` between enqueue and execution
fails with a specific message rather than silently doing nothing.

## Wave 4

### The library table's "title" is the filename without its extension

18.2 lists "title" as a column, but the unit Codarr works in is a file and
nothing in the data model carries a title: the *arrs own that, and Codarr
deliberately never asks them for metadata it does not need. Derived from the
filename.

### The Plex PIN card is the one thing on a settings page that polls

18.6 says settings pages do not poll at all. The plex.tv PIN handshake has no
other shape: you create a PIN, the user authorises it in a browser, and you poll
until a token appears. It polls at 3 seconds while its card is open and stops the
moment a token is stored. Every other settings page still polls nothing.

### `TransformRecord.video` is always present, with nullable before and after

Raised as a possible problem for a file with no video stream. It is not: the
transform builder always emits the object and leaves `before`/`after` null, so
the schema requiring the key is satisfied.

## Wave 5

### Metrics are recorded by the worker, not by the API

The API originally incremented `codarr_jobs_total` at the five seams that start
work, while the worker's own transitions were invisible. That double-counted a
manual enqueue and missed an ingest one entirely. The worker owns every job
metric now; the API only reports its own errors.

The metrics dependency is a narrow consumer-defined interface on `job.Deps` and
`promote.Deps`, and a nil value is safe at every call site. A metrics call can
never fail a job, and no test is forced to supply one.

### `codarr_bytes_*` are gauges despite the `_total` suffix

24 names them `codarr_bytes_in_total`, `codarr_bytes_out_total` and
`codarr_bytes_saved_total`, but none can be a counter: `bytes_saved` legitimately
goes negative when an AV1 source grows into HEVC (6.2), and all three are read
back out of SQLite rather than accumulated in memory, so a restart would reset a
counter the database still knows the answer to. Kept the spec's names, made them
gauges, and suppressed the linter with a comment.

### `media_files.media_info_json` is intentionally never written

18.3 wants a parsed media summary for the modal. Rather than a second column that
can drift out of step with `probe_json`, it is derived from the probe on read.
The column is left in place with a comment saying not to start writing it, since
001 is frozen.

### A settings change that needs a restart says so

`temp_dir` and `qsv_device` are read once at construction. Reading them per
operation was the alternative and was rejected: the temp dir is consulted in
preflight, which sits on the path to the pre-rename window 15.6 requires be free
of allocation and I/O, and the capability cache is keyed on the device it was
probed against (migration 003), so hot-swapping the device would answer about
hardware the encoder is not using. `PUT /api/settings` logs a warning naming
whichever changed and the General page carries a standing note.

### Overlapping roots are not a conflict; identical roots are

The frontend had grown its own conflict check that flagged nested trees. That is
wrong: `pathmap.Attribute` resolves nesting by longest prefix deliberately, so
`/media` under one instance and `/media/movies` under another is well defined.
Only an identical claim is ambiguous. The duplicate was deleted and
`pathmap.Conflicts()` is the single definition, surfaced through
`GET /api/roots` rather than a new endpoint.

## Known spec conflicts, implemented as written

Both are recorded rather than fixed, because the plan is explicit and the
consequences are bounded.

### 8.2's frame-rate term double-counts

The formula is `width * height * fps * BPP`, already linear in frame rate. 8.2
then says to additionally "scale by fps/24, capped at 1.6" for 50/60 fps. At
60 fps that compounds to roughly 4x a 24 fps target. The 8.3 ceilings bound the
damage, so in practice every affected file lands on its ceiling.

### 8.1's floor can exceed the source bitrate, which 15.3 then rejects

The clamps run in the order 8.1 gives: the 0.85 source clamp first, then the
resolution floor, then the ceiling. So a 1 Mbps 1080p source gets a 2.5 Mbps
target, above its own bitrate.

That is harmless for a copy, but a `full` plan on such a file will very likely
produce an output larger than its source, and 15.3 fails a `full` plan for
exactly that. The job fails with `verification_failed`, the staging file is
kept, the source is untouched. Safe, but the job can never succeed.

It needs a real low-bitrate source that also fails the video copy test, so a
1 Mbps 1080p VP9 or interlaced file. Rare, and it fails safe. Worth resolving
before the space sweep runs on a large library.

## Wave 5

### The worker produces every job metric, and the API produces none

`plan.md` 24 names `codarr_jobs_total{state,kind,origin}` without saying who
increments it. It was the API, at the five seams that start work, which left the
worker's own transitions invisible: nothing recorded running, verifying,
promoting, done, or a failure code, and a manual enqueue was counted at the
handler while an ingest enqueue was not counted at all.

Every transition is now recorded inside `internal/job`, which is the only place
that sees them all and the only place that knows the kind and origin of each.
`api.Metrics` shrank to `Error(category)`. Recording in both would have double
counted the API-driven half.

### Metrics are an optional dependency, nil-checked in one place

`job.Deps.Metrics` and `promote.Deps.Metrics` may be nil. Both packages funnel
every call through an unexported `recorder`, so no call site guards and no
metrics call can fail a job. Every test that does not care supplies nothing,
which keeps the nil path under test everywhere rather than in one test.

The alternative, a no-op implementation wired in the constructor, was rejected:
it makes "was this actually recorded?" invisible at the seam and it still needs
a nil check for a zero-valued `Deps`.

### The worker re-reads the queue gauges after each transition

`queue_depth` and `jobs_awaiting_stream_end` are already refreshed every 15
seconds by `metrics.Refresher` from `CountJobsByState`. The worker now runs the
same query after each transition it makes, so a change is visible on the next
scrape rather than up to a refresh later. The two writers cannot disagree,
because it is the same count. The read only happens when metrics are wired.

### `codarr_errors_total{category}` counts the errors a job survived

A failure already reports itself through `jobs_failed_total`, so counting it
again as an error would say nothing new. The categories are the places the
worker and the promoter swallow an error and carry on: `worker`, `recovery`,
`orphan_sweep`, `progress`, `staging`, `state`, `bitrate_probe`, `idet`,
`notify`, and promotion's `plex_stream_guard`. That last one is the reason this
series is worth having: 15.6 turns an unanswerable Plex into a deferral, which
is correct and completely silent.

### The root conflict rides on `GET /api/roots` rather than its own endpoint

`plan.md` 18.4 asks for a standing error when two enabled instances claim the
same root, and 20 has no endpoint for it. `pathmap.Conflicts` already computed
it and nothing called it. The listing response became
`{roots, conflicts}` because it is the same rows, derived from the same read the
settings page already makes on every load.

The frontend's own `findRootConflicts` was deleted with it. It flagged
*overlapping* trees, which is not a conflict: `pathmap.Attribute` resolves
nested roots by longest prefix, deliberately, so `/media` and `/media/movies`
owned by different instances is a well-defined configuration rather than an
ambiguous one. Only an identical claim is ambiguous, and that is what
`Conflicts` returns.

Reachability: `roots.path` is `UNIQUE` and both creation paths normalise, so two
identical rows cannot be produced through the API today. The banner is
nonetheless the honest surface for a state `Attribute` already handles, and it
is now one definition rather than two that disagreed.

### `media_files.media_info_json` stays unused, and says so

Nothing writes it; `internal/api/mediainfo.go` derives the same summary from
`probe_json` on read. Deriving is the correct choice, because a stored copy can
disagree with the probe it came from. The column keeps the schema matching
`plan.md` 17.1 and now carries a comment saying not to start populating it.

This is a comment-only edit to `001_schema.sql`, which is otherwise never
touched. It changes no DDL, and `rubenv/sql-migrate` records applied migrations
by id without a checksum, so an existing database is unaffected.

### `temp_dir` and `qsv_device` require a restart, and the UI says so

Both are read once at startup: `promote.Deps.TempDir` and the device
`hardware.New` is constructed with. Reading them per operation was the other
option and was rejected on both counts.

The temp directory is consulted inside preflight and the orphan sweep, and
preflight sits on the path to the pre-rename window that 15.6 keeps free of
allocation and I/O; adding a settings read there buys nothing and costs the one
guarantee that matters. The capability cache is keyed on the device it was
probed against (migration 003), so swapping the device under a running probe
would answer about hardware the encoder is not using.

`PUT /api/settings` logs a warning naming whichever of the two changed, and the
General settings page carries a standing note above both fields.

## Open items

Recorded in `VERIFY.md` rather than here: everything `plan.md` section 27 asks
to check against a live system.
