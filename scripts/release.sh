#!/usr/bin/env sh
# Cross-compile kuticon into dist/ for six targets.
#
#   VERSION=v0.1.0 scripts/release.sh
#   TARGETS="linux/amd64" VERSION=v0.1.0 scripts/release.sh
#
# Each target gets dist/kuticon_{version}_{os}_{arch}.tar.gz (zip on Windows)
# holding the kuticon binary, README.md and LICENSE, plus dist/SHA256SUMS.
set -eu
cd "$(dirname "$0")/.."
ver=${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}
build=${BUILD:-$(git rev-list --count HEAD 2>/dev/null || echo 0)}
targets=${TARGETS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"}
pkg=github.com/nyancodex/kuticon-cli/internal/version
ldflags="-s -w -X ${pkg}.Version=${ver} -X ${pkg}.Build=${build}"
rm -rf dist
mkdir -p dist
for t in $targets; do
  os=${t%/*}
  arch=${t#*/}
  name=kuticon_${ver}_${os}_${arch}
  stage=dist/$name
  mkdir -p "$stage"
  bin=kuticon
  [ "$os" = windows ] && bin=kuticon.exe
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$stage/$bin" ./cmd/kuticon
  cp README.md LICENSE "$stage/"
  if [ "$os" = windows ]; then
    if command -v zip >/dev/null 2>&1; then
      (cd dist && zip -qr "$name.zip" "$name")
    else
      (cd dist && python3 -m zipfile -c "$name.zip" "$name")
    fi
  else
    tar -C dist -czf "dist/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
  echo "built $t"
done
(cd dist && { command -v sha256sum >/dev/null 2>&1 && sha256sum ./*.tar.gz ./*.zip || shasum -a 256 ./*.tar.gz ./*.zip; } | sed 's# \./# #' > SHA256SUMS)
echo "dist/ ready: $(ls dist | tr '\n' ' ')"
