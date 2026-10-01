#!/usr/bin/env sh
# Builds the desktop worker zip for one platform: mangarr-worker with the
# ncnn upscalers in upscalers/ next to it, the layout the program looks for.
#
#   scripts/worker-zip.sh windows amd64 dist   -> dist/mangarr-worker-windows-amd64.zip
#
# VERSION, BUILD and COMMIT are stamped in when set. The upscaler versions
# match docker/Dockerfile.
set -eu
goos=$1 goarch=$2 out=${3:-dist}
cd "$(dirname "$0")/.."

WAIFU2X_VERSION=20250915
REALCUGAN_VERSION=20220728
REALESRGAN_RELEASE=v0.2.5.0
REALESRGAN_VERSION=20220424
case $goos in
  windows) plat=windows cugan=windows esr=windows exe=.exe ;;
  darwin) plat=macos cugan=macos esr=macos exe= ;;
  linux) plat=linux cugan=ubuntu esr=ubuntu exe= ;;
  *) echo "no worker zip for $goos" >&2; exit 1 ;;
esac

name=mangarr-worker-$goos-$goarch
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
dir=$work/$name
mkdir -p "$dir/upscalers/waifu2x" "$dir/upscalers/realcugan" "$dir/upscalers/realesrgan" "$out"
out=$(cd "$out" && pwd)

pkg=github.com/Asion001/mangarr/internal/version
CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -tags nodynamic \
  -ldflags "-s -w -X $pkg.Version=${VERSION:-dev} -X $pkg.Build=${BUILD:-local} -X $pkg.Commit=${COMMIT:-unknown}" \
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

cat > "$dir/README.txt" <<'EOF'
mangarr worker

1. On the mangarr server: System -> Workers -> Add worker, and copy the key.
2. Start mangarr-worker. Its status page opens in the browser
   (http://127.0.0.1:8790): enter the server address and the key there.

The upscalers folder holds waifu2x, Real-CUGAN and Real-ESRGAN (ncnn/Vulkan
builds by nihui and xinntao, MIT licensed); keep it next to the program.
macOS: run `xattr -dr com.apple.quarantine` on this folder once first.
More: docs/setup.md, "A worker on a desktop", in the mangarr repository.
EOF

rm -f "$out/$name.zip"
(cd "$work" && zip -qr -9 "$out/$name.zip" "$name")
echo "$out/$name.zip"
