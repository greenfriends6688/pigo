#!/bin/sh
# Build the native app. PLAN §11: one shape, ~27 MB, no node inside.
# At launch the app uses a system Node >= 22 and downloads the official
# tarball on first run when there is none (internal/nodeinstall).
# Both builds carry the 11 MB pi RPC bundle under resources/rpc.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/.." && pwd)
cd "$ROOT"

export PATH="/opt/homebrew/bin:/usr/local/go/bin:$PATH"
TARGET=${TARGET:-macos_arm64}

echo "==> check"
go tool nsgo check
echo "==> test"
go tool nsgo test

echo "==> stage RPC bundle"
sh scripts/bundle-rpc.sh

# The package never carries a node binary (PLAN §11 B′). nodeinstall.Ensure
# picks a system Node >= 22 at launch and downloads the official tarball when
# there is none, so shipping our own copy only added 85 MB to the app and
# 24 MB to the dmg.
rm -rf resources/bin

# Any node that reappears here would ship, so treat it as a build error rather
# than a silent 100 MB regression.
if [ -e resources/bin/node ]; then
  echo "build: resources/bin/node must not be packaged" >&2
  exit 1
fi

echo "==> build ($TARGET)"
go tool nsgo build -target "$TARGET"
echo "==> output in $ROOT/dist"
