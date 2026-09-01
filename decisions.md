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

## Open items

Recorded in `VERIFY.md` rather than here: everything `plan.md` section 27 asks
to check against a live system.
