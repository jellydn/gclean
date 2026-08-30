#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=${VERSION:-dev}
dist="$root/dist"
mkdir -p "$dist"
rm -f "$dist"/gclean-* "$dist/SHA256SUMS"

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
)

for target in "${targets[@]}"; do
  read -r os arch <<<"$target"
  name="gclean-${version}-${os}-${arch}"
  binary="gclean"
  if [[ $os == windows ]]; then
    binary="gclean.exe"
  fi
  work=$(mktemp -d)
  echo "Building $os/$arch"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "$work/$binary" "$root/cmd/gclean"
  if [[ $os == windows ]]; then
    (cd "$work" && zip -q "$dist/$name.zip" "$binary")
  else
    tar -C "$work" -czf "$dist/$name.tar.gz" "$binary"
  fi
  rm -rf "$work"
done

(cd "$dist" && sha256sum gclean-* > SHA256SUMS)
echo "Desktop archives written to $dist"
