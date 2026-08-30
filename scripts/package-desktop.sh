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
    if command -v zip >/dev/null 2>&1; then
      (cd "$work" && zip -q "$dist/$name.zip" "$binary")
    elif command -v python3 >/dev/null 2>&1; then
      python3 -c 'import os,sys,zipfile; z=zipfile.ZipFile(sys.argv[2], "w", zipfile.ZIP_DEFLATED); z.write(sys.argv[1], os.path.basename(sys.argv[1])); z.close()' "$work/$binary" "$dist/$name.zip"
    else
      echo "Packaging Windows requires zip or python3" >&2
      exit 1
    fi
  else
    tar -C "$work" -czf "$dist/$name.tar.gz" "$binary"
  fi
  rm -rf "$work"
done

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum gclean-* > SHA256SUMS)
else
  (cd "$dist" && shasum -a 256 gclean-* > SHA256SUMS)
fi
echo "Desktop archives written to $dist"
