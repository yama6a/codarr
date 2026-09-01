# Codarr

Single-user media transcoding service. Watches what Radarr and Sonarr produce,
works out whether each file will direct-play on Plex without server-side
transcoding, and rewrites only what needs rewriting.

One owner, one Plex server, one machine. The encoding policy is hard-coded and
not configurable. Deployment wiring lives in SQLite and is edited through the UI.

- [`plan.md`](plan.md) is the full specification
- [`decisions.md`](decisions.md) records the judgement calls made while building it
- [`VERIFY.md`](VERIFY.md) records the live-system checks

## What it does

Two goals, in order:

1. **Direct play.** Nothing in the library should force Plex to transcode video
   at playback time.
2. **Disk space.** Real, but subordinate. Where the two conflict, compatibility
   wins.

Most of the library needs no video work at all. Valid H.264 and HEVC are copied,
never upgraded, because re-encoding already-compatible video is generation loss
for no gain. The dominant job is `audio_only`: fix the DTS track, drop the PGS
subtitles, copy the video through untouched.

## There is no undo

Promotion replaces the original file with a single atomic `rename()`. Once that
completes the source is gone. There is no trash directory, no retention window
and no restore path.

Three things carry that weight instead:

- **Verification runs before promotion, always.** With no undo, it is the only
  thing standing between a bad encode and a destroyed source.
- **Anything touching more than one file dry-runs first**, shows the exact count
  and plan breakdown, and says plainly that it cannot be undone.
- **The *arrs are the recovery mechanism.** If a transcode ruins a file, Radarr
  or Sonarr can fetch it again. The library is reproducible, which is why trash
  is expendable here in a way it would not be for irreplaceable data.

## Running it

```
--db          / CODARR_DB          /data/codarr.db
--listen      / CODARR_LISTEN      :8080
--log-level   / CODARR_LOG_LEVEL   info
--ffmpeg      / CODARR_FFMPEG      ffmpeg
--ffprobe     / CODARR_FFPROBE     ffprobe
```

Everything else lives in the database and is edited in the UI. On first run with
an empty database it starts with no Plex, no *arr instances and no roots; ingest
does nothing until at least one root exists.

**There is no authentication.** Access is secured externally. There is no login,
no API key check and no session handling, deliberately.

## Development

```bash
make ci        # fumpt, generate, lint, vet, govulncheck, go test ./...
make build     # frontend, then the binary with it embedded
make run       # against ./data/codarr.db
make web-dev   # Vite dev server, proxies /api to the Go server
make image     # amd64 image
```

## Deployment

The image is `ghcr.io/yama6a/codarr:<N>`, integer-tagged, one release per merge
to `main`, amd64 only.

This repo ships no Kubernetes manifests, but it does bump its own tag: on a push
to `main`, `build-push` opens a PR against `offgrid-private` pinning the new
image in the media chart and arms an auto-merge behind that repo's checks. It
needs a `DEPLOY_TOKEN` secret.

A failed check there leaves the PR open and silent, and a forgotten open PR is a
tag pinned at an old version. Watch for them.

The wiring below is what that chart already contains, kept here because the
cluster repo is where it lives but this is where the reasoning belongs.

### Chart placement

`argo_apps/workloads/charts/media/templates/codarr.yaml`, with a `codarr:` block
in that chart's `values.yaml`. Not a standalone chart: a PVC cannot be mounted
across namespaces, and both `media-library` and the config volumes live in
`media`.

### GPU

```yaml
resources:
  limits:
    gpu.intel.com/i915: "1"
```

Nothing else. No `nodeSelector`, no `/dev/dri` hostPath, no
`supplementalGroups`. The Intel device plugin advertises the resource only on
the node carrying `extensions.talos.dev/i915`, so requesting it is what pins the
pod to `tc-w1`. See that repo's `docs/14_igpu.md`.

`sharedDevNum` is 2 and Plex already holds one slot, so Codarr takes the last
one. A third claimant would sit `Pending` on `Insufficient gpu.intel.com/i915`.

Verified on the node: QSV and VAAPI both encode HEVC Main and Main10, VP9
hardware decode works, and the driver is the jellyfin-bundled Intel iHD 25.4.6.
See `VERIFY.md`.

### Volumes

| Volume | Claim | Mount | Notes |
|---|---|---|---|
| library | `media-library` | `/media` | **No `subPath`.** Codarr needs both owners' trees |
| config | `codarr-config` | `/data` | `longhorn-r2-retained-with-backups`, RWO, 5Gi |

`media-downloads` is not mounted. Codarr never touches the staging area.

`replicas: 1` and `strategy: Recreate`, because two processes on one SQLite
directory corrupt it.

5Gi is generous for the config volume. The database holds one row per file with
its full ffprobe output, plus a transform record per job, so a library of a few
thousand files lands in the low hundreds of megabytes. `tc-w1` had 854Gi of
Longhorn storage available and 188Gi scheduled when this was written, so there is
room; `docs/30_media.md`'s claim of roughly 64Gi of headroom is out of date.

**Staging never touches this volume.** Codarr writes its output as a dotfile
sibling of the target on the NAS, so `rename()` is a single atomic server-side
operation. That was verified on the real mount: renaming from a local volume into
`/media` returns EXDEV.

### Security context

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 568
  runAsGroup: 568
  fsGroup: 568
  fsGroupChangePolicy: OnRootMismatch
  seccompProfile: {type: RuntimeDefault}
```

uid 568 is what owns the NAS dataset. `fsGroup` does not apply to the NFS mount
at all; it is there for the RWO config volume.

### Ingress

Adding a host means all three, or the SecurityPolicy attaches to nothing and the
app is served unauthenticated:

1. `media/values.yaml`, under `ingress.ingresses[].hosts`
2. `04_google_sso/values.yaml`, as `codarr.media` under `yama.casa`
3. `05_blackbox_exporter/values.yaml`, under `probes.workloads-sso.targets`

Plus a `CiliumNetworkPolicy` entry in `media/templates/networkpolicy.yaml`.

### Chart details

- Image pin with `# renovate: datasource=docker depName=ghcr.io/yama6a/codarr`
  on the preceding line. Tags stay pinned: a floating tag would deploy on its
  own schedule.
- Add `ghcr.io/yama6a/codarr` to `SKIP_IMAGES` in
  `lib/shell/check_multiarch.sh`, after the amd64 pin is in the chart.
- Pod labels: `app: codarr`, `alert-criticality: warning`,
  `longhorn-replica-affinity/enabled: "true"`.

### Changes outside Codarr

**A Plex client profile for HEVC in browsers.** Without it, Plex Media Server
transcodes HEVC to browser clients regardless of what the browser can decode,
because it decides from its own client profile rather than browser capability.
The profile is in `plan.md` section 23.1; drop it at
`{plex data dir}/Profiles/Chrome.xml` and restart the Plex pod.

**Upgrades must stay disabled on every Radarr and Sonarr instance.** That is
what makes `full` jobs safe: with upgrades off, a rescan cannot trigger an
upgrade search regardless of what the refreshed mediainfo says.

**The naming formats must stay free of `{MediaInfo ...}` tokens.** They are, as
of 2026-09-01: title, year and id only. That is what lets renaming stay on
safely. Reintroducing a codec or audio token would make a `full` job change the
name an *arr wants, and the next rename pass would churn every path Codarr had
touched.

Also worth fixing while in that repo: `docs/30_media.md:335` still says hardware
transcoding is not wired up. The device plugin is installed and Plex holds a
slot, so that paragraph is stale.
