#!/bin/sh
# Copy pi's prebuilt RPC bundle into resources/rpc so the packaged .app does
# not depend on a global pi installation. At runtime agent.ResolveBundle
# prefers resources/rpc/rpc-entry.js and falls back to the global install.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$HERE/.." && pwd)
DEST="$ROOT/resources/rpc"
NODE=${NODE:-node}

SRC=""
for candidate in \
  "$HOME/.npm-global/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle" \
  "$(CDPATH= cd -- "$ROOT/.." 2>/dev/null && pwd)/node_modules/@earendil-works/pi-coding-agent/dist/bundle"; do
  if [ -f "$candidate/rpc-entry.js" ]; then SRC="$candidate"; break; fi
done

if [ -z "$SRC" ]; then
  # Resolve pi's real path through node when the global layout differs.
  resolved=$($NODE -e 'const fs=require("fs"),p=require("child_process");try{const s=fs.realpathSync(p.execFileSync("which",["pi"],{encoding:"utf8"}).trim());let d=require("path").dirname(s);for(let i=0;i<6;i++){const c=require("path").join(d,"dist","bundle","rpc-entry.js");if(fs.existsSync(c)){process.stdout.write(require("path").dirname(c));break}d=require("path").dirname(d)}}catch(e){}' 2>/dev/null || true)
  [ -n "$resolved" ] && SRC="$resolved"
fi

if [ -z "$SRC" ] || [ ! -f "$SRC/rpc-entry.js" ]; then
  echo "bundle-rpc: cannot find pi's dist/bundle/rpc-entry.js" >&2
  exit 1
fi

rm -rf "$DEST"
mkdir -p "$DEST"
cp -R "$SRC/." "$DEST/"
chmod +x "$DEST/rpc-entry.js" 2>/dev/null || true

# The bundle is ESM but has no package.json, so Node re-parses every chunk as
# a module on each spawn (MODULE_TYPELESS_PACKAGE_JSON) and looks for a
# package.json all the way up to $HOME. Declaring the type here removes the
# warning and the extra parse.
printf '{"type":"module"}\n' > "$DEST/package.json"

# The EPIPE guard rides beside the bundle and is loaded with --require, so it
# survives a re-copy of pi's output instead of patching minified chunks.
cp "$HERE/epipe-guard.cjs" "$DEST/epipe-guard.cjs"

# pi loads TypeScript extensions (and .ts skills) through jiti, which the
# bundle resolves at runtime from <bundle>/chunks/...; without it every
# extension fails and the RPC child exits 1 before answering get_state.
JITI=""
for candidate in \
  "$SRC/../../node_modules/jiti" \
  "$HOME/.npm-global/lib/node_modules/@earendil-works/pi-coding-agent/node_modules/jiti"; do
  if [ -d "$candidate" ]; then JITI="$candidate"; break; fi
done
if [ -n "$JITI" ]; then
  mkdir -p "$DEST/node_modules"
  cp -R "$JITI" "$DEST/node_modules/jiti"
  echo "bundle-rpc: jiti $(du -sh "$DEST/node_modules/jiti" | cut -f1)"
else
  echo "bundle-rpc: warn: jiti not found; TypeScript extensions will fail to load" >&2
fi

echo "bundle-rpc: $(du -sh "$DEST" | cut -f1) copied from $SRC"
