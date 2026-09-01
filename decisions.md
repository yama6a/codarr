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

## Open items

Recorded in `VERIFY.md` rather than here: everything `plan.md` section 27 asks
to check against a live system.
