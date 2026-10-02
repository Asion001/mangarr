#!/usr/bin/env sh
# Builds the desktop worker zip for one platform: mangarr-worker with the
# ncnn upscalers in upscalers/ and the native encoders (avifenc, cwebp, and
# cjxl on Linux) in encoders/ next to it, the layout the program looks for.
#
#   scripts/worker-zip.sh windows amd64 dist   -> dist/mangarr-worker-windows-amd64.zip
#                                                 dist/mangarr-worker-windows-amd64.zip.sha256
#
# The checksum is what a worker checks the zip against before it updates
# itself (internal/workerupdate).
#
# VERSION, BUILD, COMMIT, UPDATE_URL and IMAGE are stamped in when set. The upscaler versions
# match docker/Dockerfile; the encoders are the projects' own release builds.
set -eu
goos=$1 goarch=$2 out=${3:-dist}
cd "$(dirname "$0")/.."

WAIFU2X_VERSION=20250915
REALCUGAN_VERSION=20220728
REALESRGAN_RELEASE=v0.2.5.0
REALESRGAN_VERSION=20220424
LIBAVIF_VERSION=1.4.2
LIBWEBP_VERSION=1.6.0
LIBJXL_VERSION=0.11.1
case $goos in
  windows) plat=windows cugan=windows esr=windows avif=windows webp=windows-x64 exe=.exe ;;
  darwin) plat=macos cugan=macos esr=macos avif=macOS webp=mac-arm64 exe= ;;
  linux) plat=linux cugan=ubuntu esr=ubuntu avif=linux webp=linux-x86-64 exe= ;;
  *) echo "no worker zip for $goos" >&2; exit 1 ;;
esac

name=mangarr-worker-$goos-$goarch
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
dir=$work/$name
mkdir -p "$dir/upscalers/waifu2x" "$dir/upscalers/realcugan" "$dir/upscalers/realesrgan" "$dir/encoders" "$out"
out=$(cd "$out" && pwd)

pkg=github.com/Asion001/mangarr/internal/version
CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -tags nodynamic \
  -ldflags "-s -w -X $pkg.Version=${VERSION:-dev} -X $pkg.Build=${BUILD:-local} -X $pkg.Commit=${COMMIT:-unknown} -X $pkg.UpdateURL=${UPDATE_URL:-} -X $pkg.Image=${IMAGE:-}" \
  -o "$dir/mangarr-worker$exe" ./cmd/mangarr-worker

fetch() { # url, folder: unzip a release into upscalers/<folder>, without its top folder
  curl -fsSL -o "$work/z.zip" "$1"
  rm -rf "$work/z" && mkdir "$work/z" && unzip -q "$work/z.zip" -d "$work/z"
  src=$work/z
  if [ "$(ls "$src" | wc -l)" -eq 1 ] && [ -d "$src/$(ls "$src")" ]; then src=$src/$(ls "$src"); fi
  # the demo images and video aren't needed
  rm -f "$src"/*.jpg "$src"/*.png "$src"/*.mp4
  cp -R "$src"/. "$dir/upscalers/$2/"
}
fetch "https://github.com/nihui/waifu2x-ncnn-vulkan/releases/download/$WAIFU2X_VERSION/waifu2x-ncnn-vulkan-$WAIFU2X_VERSION-$plat.zip" waifu2x
fetch "https://github.com/nihui/realcugan-ncnn-vulkan/releases/download/$REALCUGAN_VERSION/realcugan-ncnn-vulkan-$REALCUGAN_VERSION-$cugan.zip" realcugan
fetch "https://github.com/xinntao/Real-ESRGAN/releases/download/$REALESRGAN_RELEASE/realesrgan-ncnn-vulkan-$REALESRGAN_VERSION-$esr.zip" realesrgan
# only the models mangarr offers (internal/upscaler/engines.go)
rm -rf "$dir/upscalers/waifu2x/models-upconv_7_photo" "$dir/upscalers/realcugan/models-pro" "$dir/upscalers/realcugan/models-nose" \
  "$dir/upscalers/realesrgan/models/realesrgan-x4plus."*
chmod +x "$dir"/upscalers/*/*-ncnn-vulkan* 2>/dev/null || true

# native encoders: several times faster than the built-in AVIF encoder, and
# they use every core on tall pages
curl -fsSL -o "$work/a.zip" "https://github.com/AOMediaCodec/libavif/releases/download/v$LIBAVIF_VERSION/$avif-artifacts.zip"
unzip -q -j -o "$work/a.zip" "avifenc$exe" -d "$dir/encoders"
if [ "$goos" = windows ]; then
  curl -fsSL -o "$work/w.zip" "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-$LIBWEBP_VERSION-$webp.zip"
  unzip -q -j -o "$work/w.zip" "*/bin/cwebp.exe" -d "$dir/encoders"
else
  curl -fsSL "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-$LIBWEBP_VERSION-$webp.tar.gz" |
    tar -xz -C "$work" --wildcards "*/bin/cwebp"
  cp "$work"/libwebp-*/bin/cwebp "$dir/encoders/"
fi
if [ "$goos" = linux ]; then # libjxl publishes a static cjxl for Linux only
  curl -fsSL "https://github.com/libjxl/libjxl/releases/download/v$LIBJXL_VERSION/jxl-linux-x86_64-static-v$LIBJXL_VERSION.tar.gz" |
    tar -xz -C "$work" tools/cjxl
  cp "$work/tools/cjxl" "$dir/encoders/"
fi
chmod +x "$dir"/encoders/* 2>/dev/null || true

cat > "$dir/README.txt" <<'EOF'
mangarr worker

1. On the mangarr server: System -> Workers -> Add worker, and copy the key.
2. Start mangarr-worker. Its status page opens in the browser
   (http://127.0.0.1:8790): enter the server address and the key there.

The upscalers folder holds waifu2x, Real-CUGAN and Real-ESRGAN (ncnn/Vulkan
builds by nihui and xinntao, MIT licensed); the encoders folder holds
avifenc (libavif), cwebp (libwebp) and on Linux cjxl (libjxl), all
BSD licensed. Keep both folders next to the program.
macOS: run `xattr -dr com.apple.quarantine` on this folder once first.
More: docs/setup.md, "A worker on a desktop", in the mangarr repository.
EOF

rm -f "$out/$name.zip"
(cd "$work" && zip -qr -9 "$out/$name.zip" "$name")
if command -v sha256sum >/dev/null 2>&1; then sum=$(sha256sum "$out/$name.zip"); else sum=$(shasum -a 256 "$out/$name.zip"); fi
echo "${sum%% *}  $name.zip" > "$out/$name.zip.sha256"
echo "$out/$name.zip"
