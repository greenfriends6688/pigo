// Loaded with `node --require epipe-guard.cjs <rpc-entry.js>`.
//
// When the Go parent closes the JSONL pipe (session switch, quit, reconnect),
// pi's writeRawStdoutChunk can still have frames in flight. Node turns that
// into an unhandled 'error' event on stdout and aborts the child with
// "Error: write EPIPE", which the window reports as a lost connection. This
// guard exits quietly on that one expected condition; everything else stays
// fatal. It runs before the bundle is evaluated, so it also covers the npm
// fallback bundle that ships without a patched entry.
for (const stream of [process.stdout, process.stderr]) {
  stream.on("error", (err) => {
    if (err && (err.code === "EPIPE" || err.code === "ERR_STREAM_DESTROYED")) process.exit(0);
    throw err;
  });
}
