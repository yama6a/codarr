# --- frontend ---
# Built here rather than on the runner: go:embed needs internal/web/dist populated before `go build`,
# and this image is single-arch so there is nothing to share across platforms.
FROM node:26-alpine@sha256:40cd011b55415bc894e45b02fdd5a5dfde056921b7d23a64c74f80497af48797 AS web
# The repo layout is mirrored so vite's outDir of ../internal/web/dist lands where the build stage
# expects it.
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# --- backend ---
# Build runs on the builder's native arch and cross-compiles to the target (TARGETOS/TARGETARCH come
# from buildx), so no QEMU emulation is needed.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
ARG TARGETOS TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,id=go-mod go mod download

COPY . .
COPY --from=web /src/internal/web/dist ./internal/web/dist

RUN --mount=type=cache,target=/root/.cache/go-build,id=go-build-$TARGETARCH \
    --mount=type=cache,target=/go/pkg/mod,id=go-mod \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /codarr ./cmd/codarr

# --- runtime ---
FROM debian:bookworm-slim@sha256:99c0b25c46d0214d5a2091092e7908907dcaecd6a962cde0ac700acee222b6f3

# The jellyfin key is fetched through a pipe, and the default /bin/sh -c would let curl fail
# silently while gpg still succeeds.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# intel-media-va-driver-non-free lives in Debian's non-free component, which the stock image does not
# enable. Without this sed the build fails at apt-get.
# jellyfin-ffmpeg7 rather than distro ffmpeg: it is patched for this workload and handles HDR metadata
# better. intel-media-va-driver-non-free is required for QSV on Gen 9.5; the free driver will not do
# HEVC encode. See plan.md 22.
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

# 568 matches the NAS dataset ownership. No supplementary group is needed for /dev/dri: Talos udev
# makes the render nodes mode 0666 group 0, and the Intel device plugin injects the device on the
# `gpu.intel.com/i915` request. See offgrid-private docs/14_igpu.md.
USER 568:568

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/codarr"]
