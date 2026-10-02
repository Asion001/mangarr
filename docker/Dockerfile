# syntax=docker/dockerfile:1
#
# One image for every role (MANGARR_MODE=integrated|server|worker).
#
#   target full (default, :latest)  Debian + Mesa Vulkan + avifenc/cjxl/cjpeg + the
#                                   ncnn upscalers (amd64; arm64 has everything
#                                   but the upscalers, which are x86-only)
#   target slim (:slim)             distroless server only (~35 MB); re-encodes
#                                   with the slower built-in AVIF encoder
#
# Pass a GPU with `devices: [/dev/dri:/dev/dri]` (Intel/AMD; add the render
# group with group_add) or the NVIDIA container toolkit.

# ---- web UI ------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- Go binary -----------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG BUILD=local
ARG COMMIT=unknown
# where this build's worker zips and image are published, for the updates
# offered to workers (internal/version)
ARG UPDATE_URL=
ARG IMAGE=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags nodynamic \
    -ldflags "-s -w -X github.com/Asion001/mangarr/internal/version.Version=${VERSION} -X github.com/Asion001/mangarr/internal/version.Build=${BUILD} -X github.com/Asion001/mangarr/internal/version.Commit=${COMMIT} -X github.com/Asion001/mangarr/internal/version.UpdateURL=${UPDATE_URL} -X github.com/Asion001/mangarr/internal/version.Image=${IMAGE}" \
    -o /out/mangarr ./cmd/mangarr && mkdir -p /out/config
# nodynamic keeps the built-in AVIF encoder from loading a system libavif
# (which would make the binary dynamically linked and break distroless)

# ---- progressive AVIF encoder ---------------------------------------------------
# Debian trixie ships libavif 1.2, while layered encoding was added in 1.4.
# Build the app statically into avifenc and use Debian's codec libraries at runtime.
FROM debian:trixie-slim AS avif
ARG LIBAVIF_VERSION=1.4.2
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential ca-certificates cmake curl git ninja-build pkg-config \
      libaom-dev libjpeg62-turbo-dev libpng-dev libyuv-dev \
    && curl -fsSL "https://github.com/AOMediaCodec/libavif/archive/refs/tags/v${LIBAVIF_VERSION}.tar.gz" -o /tmp/libavif.tar.gz \
    && tar -xzf /tmp/libavif.tar.gz -C /tmp \
    && cmake -S "/tmp/libavif-${LIBAVIF_VERSION}" -B /tmp/libavif-build -G Ninja \
      -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DAVIF_BUILD_APPS=ON \
      -DAVIF_CODEC_AOM=SYSTEM -DAVIF_LIBYUV=SYSTEM -DAVIF_JPEG=SYSTEM -DAVIF_ZLIBPNG=SYSTEM \
    && cmake --build /tmp/libavif-build --target avifenc \
    && install -Dm755 /tmp/libavif-build/avifenc /out/avifenc

# ---- ncnn upscalers (x86_64 binaries) -------------------------------------------
FROM debian:trixie-slim AS tools
ARG TARGETARCH
ARG WAIFU2X_VERSION=20250915
ARG REALCUGAN_VERSION=20220728
ARG REALESRGAN_RELEASE=v0.2.5.0
ARG REALESRGAN_VERSION=20220424
RUN set -eux; mkdir -p /opt/upscalers; \
    if [ "$TARGETARCH" != "amd64" ]; then echo "no ncnn upscalers for $TARGETARCH"; exit 0; fi; \
    apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip; \
    cd /tmp; \
    curl -fsSL -o w.zip "https://github.com/nihui/waifu2x-ncnn-vulkan/releases/download/${WAIFU2X_VERSION}/waifu2x-ncnn-vulkan-${WAIFU2X_VERSION}-linux.zip"; \
    unzip -q w.zip -d w; mv w/*/ /opt/upscalers/waifu2x; \
    curl -fsSL -o c.zip "https://github.com/nihui/realcugan-ncnn-vulkan/releases/download/${REALCUGAN_VERSION}/realcugan-ncnn-vulkan-${REALCUGAN_VERSION}-ubuntu.zip"; \
    unzip -q c.zip -d c; mv c/*/ /opt/upscalers/realcugan; \
    curl -fsSL -o r.zip "https://github.com/xinntao/Real-ESRGAN/releases/download/${REALESRGAN_RELEASE}/realesrgan-ncnn-vulkan-${REALESRGAN_VERSION}-ubuntu.zip"; \
    mkdir -p /opt/upscalers/realesrgan; unzip -q r.zip -d /opt/upscalers/realesrgan; \
    chmod +x /opt/upscalers/*/*-ncnn-vulkan; \
    rm -rf /var/lib/apt/lists/* /tmp/*

# ---- slim: server only ------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS slim
COPY --from=build /out/mangarr /mangarr
COPY --from=build --chown=65532:65532 /out/config /config
ENV MANGARR_DATA_DIR=/config \
    MANGARR_LISTEN=:8787 \
    MANGARR_MODE=server
EXPOSE 8787 25600
VOLUME ["/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["/mangarr", "healthcheck"]
ENTRYPOINT ["/mangarr"]

# ---- full: every role ----------------------------------------------------------------
FROM debian:trixie-slim AS full
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata libvulkan1 mesa-vulkan-drivers libgomp1 libegl1 vulkan-tools \
      webp libavif-bin libjxl-tools libjpeg-progs \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 65532 --gid 0 --home /config mangarr \
    && mkdir -p /config && chown 65532:0 /config
COPY --from=tools /opt/upscalers /opt/upscalers
COPY --from=build /out/mangarr /usr/local/bin/mangarr
COPY --from=avif /out/avifenc /usr/local/bin/avifenc
RUN ln -s mangarr /usr/local/bin/mangarr-worker
ENV MANGARR_DATA_DIR=/config \
    MANGARR_LISTEN=:8787 \
    MANGARR_MODE=integrated \
    MANGARR_UPSCALER_TOOLS_DIR=/opt/upscalers
EXPOSE 8787 25600
VOLUME ["/config"]
USER 65532
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD ["mangarr", "healthcheck"]
ENTRYPOINT ["mangarr"]
