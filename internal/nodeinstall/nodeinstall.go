// Package nodeinstall locates a usable Node.js 22+ runtime for the desktop app.
//
// The desktop app requires system Node.js >= 22. When no suitable runtime is
// found, Ensure downloads the newest Node.js 22 LTS release from nodejs.org,
// verifies its SHA-256 checksum and unpacks it below the given directory.
package nodeinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// minMajor is the lowest supported Node.js major version (B' default).
const minMajor = 22

// Progress stages.
const (
	stageChecking    = "checking"
	stageDownloading = "downloading"
	stageVerifying   = "verifying"
	stageExtracting  = "extracting"
	stageReady       = "ready"
	stageFailed      = "failed"
)

const (
	distBase   = "https://nodejs.org/dist"
	indexURL   = distBase + "/index.json"
	maxTarball = 120 << 20 // 120 MiB hard limit
	maxIndex   = 8 << 20
	maxShasums = 1 << 20

	// versionTimeout bounds every short `node --version` style probe so a
	// broken runtime can never block the caller.
	versionTimeout = 15 * time.Second
)

// httpClient is used for all nodejs.org requests. The overall timeout keeps a
// stalled connection from hanging the desktop startup path forever; ctx passed
// by the caller provides prompt cancellation.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// Progress reports the state of an Ensure call. Stage is one of "checking",
// "downloading", "verifying", "extracting", "ready" or "failed"; Percent is
// 0..100; Detail is a human readable message; Err is set when Stage is "failed".
type Progress struct {
	Stage   string
	Percent int
	Detail  string
	Err     error
}

// SystemNodeAtLeast22 looks for a system Node.js binary and reports whether it
// is at least major version 22. It searches PATH first and falls back to
// `$SHELL -lc 'command -v node'` so runtimes installed by version managers
// (nvm, fnm, asdf, ...) are found even when the GUI app did not inherit the
// shell PATH. It never returns an error: an unusable runtime is reported via
// ok == false together with whatever path/version could be determined.
func SystemNodeAtLeast22() (path, version string, ok bool) {
	p, err := exec.LookPath("node")
	if err != nil || p == "" {
		p = nodeFromLoginShell()
	}
	if p == "" {
		return "", "", false
	}
	if abs, absErr := filepath.Abs(p); absErr == nil {
		p = abs
	}
	v, err := nodeVersion(p)
	if err != nil {
		return p, "", false
	}
	v = strings.TrimSpace(v)
	major, _, _, err := ParseVersion(v)
	if err != nil || major < minMajor {
		return p, v, false
	}
	return p, v, true
}

// Ensure returns the path of a Node.js binary with major version >= 22.
//
// Resolution order:
//  1. a system Node.js from SystemNodeAtLeast22;
//  2. an already unpacked runtime at dir/node-v*/bin/node;
//  3. the newest Node.js 22 release downloaded from nodejs.org.
//
// dir is created if missing. onProgress may be nil. Cancelling ctx aborts the
// download and removes any temporary files that were created.
func Ensure(ctx context.Context, dir string, onProgress func(Progress)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("nodeinstall: resolve cache dir: %w", err)
		}
		dir = filepath.Join(cache, "pi-web", "node")
	}

	last := 0
	send := func(stage string, percent int, detail string) {
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		last = percent
		if onProgress != nil {
			onProgress(Progress{Stage: stage, Percent: percent, Detail: detail})
		}
	}
	failed := func(err error) (string, error) {
		if onProgress != nil {
			onProgress(Progress{Stage: stageFailed, Percent: last, Detail: err.Error(), Err: err})
		}
		return "", err
	}

	send(stageChecking, 0, "looking for a system Node.js >= "+strconv.Itoa(minMajor))
	if p, v, ok := SystemNodeAtLeast22(); ok {
		send(stageReady, 100, "using system Node.js "+v)
		return p, nil
	}

	if p, v, ok := reuseInstalled(dir); ok {
		send(stageReady, 100, "using installed Node.js "+v)
		return p, nil
	}

	_, ext, err := distTarget(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return failed(err)
	}
	if ext != ".tar.gz" {
		return failed(fmt.Errorf("nodeinstall: automatic install on %s/%s requires .zip archives, which are not supported", runtime.GOOS, runtime.GOARCH))
	}

	send(stageChecking, 2, "downloading release index")
	index, err := httpGet(ctx, indexURL, maxIndex, nil)
	if err != nil {
		return failed(err)
	}

	tarURL, filename, version, err := LatestV22URL(index, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return failed(err)
	}

	send(stageDownloading, 5, "downloading "+filename)
	tarball, err := httpGet(ctx, tarURL, maxTarball, func(p int) {
		send(stageDownloading, 5+p*67/100, "downloading "+filename)
	})
	if err != nil {
		return failed(err)
	}

	send(stageVerifying, 78, "verifying checksum of "+filename)
	shasums, err := httpGet(ctx, distBase+"/"+version+"/SHASUMS256.txt", maxShasums, nil)
	if err != nil {
		return failed(err)
	}
	if err := VerifySHA256(tarball, string(shasums), filename); err != nil {
		return failed(err)
	}

	send(stageExtracting, 88, "extracting "+filename)
	nodePath, err := installTarball(dir, filename, tarball)
	if err != nil {
		return failed(err)
	}

	v, err := nodeVersion(nodePath)
	if err != nil {
		return failed(fmt.Errorf("nodeinstall: verify installed node: %w", err))
	}
	major, _, _, err := ParseVersion(v)
	if err != nil || major < minMajor {
		return failed(fmt.Errorf("nodeinstall: installed node %q is older than %d", strings.TrimSpace(v), minMajor))
	}

	send(stageExtracting, 98, "installed Node.js "+strings.TrimSpace(v))
	if err := ctx.Err(); err != nil {
		return failed(fmt.Errorf("nodeinstall: cancelled: %w", err))
	}
	send(stageReady, 100, "using downloaded Node.js "+strings.TrimSpace(v))
	return nodePath, nil
}

// ParseVersion parses a Node.js style version string such as "v22.16.0",
// "22.16.0" or "v22.16.0-rc.1" and returns its numeric components.
func ParseVersion(s string) (major, minor, patch int, err error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return 0, 0, 0, fmt.Errorf("nodeinstall: invalid version %q", orig)
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("nodeinstall: invalid version %q: want major.minor.patch", orig)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		if p == "" || !isDigits(p) {
			return 0, 0, 0, fmt.Errorf("nodeinstall: invalid version %q: bad component %q", orig, p)
		}
		n, convErr := strconv.Atoi(p)
		if convErr != nil {
			return 0, 0, 0, fmt.Errorf("nodeinstall: invalid version %q: %w", orig, convErr)
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], nil
}

// LatestV22URL selects the newest stable Node.js 22 release that ships an
// archive for goos/goarch from the nodejs.org index.json payload and returns
// its download URL, file name and version (for example "v22.16.0").
func LatestV22URL(indexJSON []byte, goos, goarch string) (url, filename, version string, err error) {
	platform, ext, err := distTarget(goos, goarch)
	if err != nil {
		return "", "", "", err
	}
	keys, err := fileKeys(goos, goarch)
	if err != nil {
		return "", "", "", err
	}

	var entries []struct {
		Version string   `json:"version"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal(indexJSON, &entries); err != nil {
		return "", "", "", fmt.Errorf("nodeinstall: parse index.json: %w", err)
	}
	if len(entries) == 0 {
		return "", "", "", fmt.Errorf("nodeinstall: parse index.json: empty release list")
	}

	bestMaj, bestMin, bestPat := -1, -1, -1
	found := false
	for _, e := range entries {
		if !hasAnyFile(e.Files, keys) {
			continue
		}
		if strings.Contains(strings.TrimPrefix(e.Version, "v"), "-") {
			continue // skip pre-releases such as v22.0.0-rc.1
		}
		maj, min, pat, perr := ParseVersion(e.Version)
		if perr != nil || maj != minMajor {
			continue
		}
		if !found || maj > bestMaj || (maj == bestMaj && (min > bestMin || (min == bestMin && pat > bestPat))) {
			bestMaj, bestMin, bestPat, found = maj, min, pat, true
		}
	}
	if !found {
		return "", "", "", fmt.Errorf("nodeinstall: no Node.js %d release for %s/%s in index.json", minMajor, goos, goarch)
	}

	version = fmt.Sprintf("v%d.%d.%d", bestMaj, bestMin, bestPat)
	filename = "node-" + version + "-" + platform + ext
	return distBase + "/" + version + "/" + filename, filename, version, nil
}

// VerifySHA256 checks that tarball matches filename's SHA-256 digest found in
// a SHASUMS256.txt style manifest ("<hex digest>  <file name>" per line).
func VerifySHA256(tarball []byte, shasums string, filename string) error {
	want := ""
	for _, line := range strings.Split(shasums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == filename {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("nodeinstall: checksum for %q not found in SHASUMS256.txt", filename)
	}
	if len(want) != sha256.Size*2 {
		return fmt.Errorf("nodeinstall: invalid checksum %q for %q", want, filename)
	}
	sum := sha256.Sum256(tarball)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("nodeinstall: checksum mismatch for %q: got %s, want %s", filename, got, want)
	}
	return nil
}

// distTarget maps a Go GOOS/GOARCH pair to the nodejs.org platform name and
// archive extension. Windows only publishes .zip archives, so callers must
// treat the returned ".zip" extension as unsupported.
func distTarget(goos, goarch string) (platform, ext string, err error) {
	switch goos + "/" + goarch {
	case "darwin/arm64":
		return "darwin-arm64", ".tar.gz", nil
	case "darwin/amd64":
		return "darwin-x64", ".tar.gz", nil
	case "linux/amd64":
		return "linux-x64", ".tar.gz", nil
	case "linux/arm64":
		return "linux-arm64", ".tar.gz", nil
	case "windows/amd64":
		return "win-x64", ".zip", nil
	default:
		return "", "", fmt.Errorf("nodeinstall: unsupported platform %s/%s", goos, goarch)
	}
}

// fileKeys returns the index.json "files" entries that prove an archive exists
// for goos/goarch. nodejs.org has used both "<platform>-tar" and the bare
// platform name over time, plus "osx-*" aliases on macOS.
func fileKeys(goos, goarch string) ([]string, error) {
	platform, _, err := distTarget(goos, goarch)
	if err != nil {
		return nil, err
	}
	keys := []string{platform + "-tar", platform + "-zip", platform}
	if strings.HasPrefix(platform, "darwin-") {
		alias := "osx-" + strings.TrimPrefix(platform, "darwin-")
		keys = append(keys, alias+"-tar", alias+"-zip", alias)
	}
	return keys, nil
}

func hasAnyFile(files, keys []string) bool {
	for _, f := range files {
		for _, k := range keys {
			if f == k {
				return true
			}
		}
	}
	return false
}

// reuseInstalled returns the newest usable runtime already unpacked below dir.
func reuseInstalled(dir string) (path, version string, ok bool) {
	matches, err := filepath.Glob(filepath.Join(dir, "node-v*"))
	if err != nil {
		return "", "", false
	}
	type candidate struct {
		path         string
		version      string
		major, minor int
		patch        int
	}
	var found []candidate
	for _, m := range matches {
		st, statErr := os.Stat(m)
		if statErr != nil || !st.IsDir() {
			continue
		}
		bin := filepath.Join(m, "bin", "node")
		fi, statErr := os.Stat(bin)
		if statErr != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			continue
		}
		v, verr := nodeVersion(bin)
		if verr != nil {
			continue
		}
		v = strings.TrimSpace(v)
		maj, min, pat, perr := ParseVersion(v)
		if perr != nil || maj < minMajor {
			continue
		}
		found = append(found, candidate{path: bin, version: v, major: maj, minor: min, patch: pat})
	}
	if len(found) == 0 {
		return "", "", false
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.major != b.major {
			return a.major > b.major
		}
		if a.minor != b.minor {
			return a.minor > b.minor
		}
		return a.patch > b.patch
	})
	return found[0].path, found[0].version, true
}

// installTarball unpacks the archive into a temporary directory below dir and
// moves the resulting node-vX.Y.Z-<platform> tree into place.
func installTarball(dir, filename string, tarball []byte) (string, error) {
	base := strings.TrimSuffix(filename, ".tar.gz")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("nodeinstall: create install dir: %w", err)
	}
	tmp, err := os.MkdirTemp(dir, ".nodeinstall-")
	if err != nil {
		return "", fmt.Errorf("nodeinstall: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := extractTarGz(tmp, bytes.NewReader(tarball)); err != nil {
		return "", err
	}

	src := filepath.Join(tmp, base)
	if st, statErr := os.Stat(src); statErr != nil || !st.IsDir() {
		return "", fmt.Errorf("nodeinstall: archive does not contain %q", base)
	}

	dest := filepath.Join(dir, base)
	if err := os.RemoveAll(dest); err != nil {
		return "", fmt.Errorf("nodeinstall: replace %q: %w", base, err)
	}
	if err := os.Rename(src, dest); err != nil {
		return "", fmt.Errorf("nodeinstall: move %q into place: %w", base, err)
	}
	return filepath.Join(dest, "bin", "node"), nil
}

// nodeVersion runs "<bin> --version" and returns its raw output.
func nodeVersion(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("nodeinstall: run %s --version: %w", bin, err)
	}
	return string(out), nil
}

// nodeFromLoginShell asks the user's login shell where node lives.
func nodeFromLoginShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lc", "command -v node").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if st, statErr := os.Stat(line); statErr == nil && !st.IsDir() {
			return line
		}
	}
	return ""
}

// httpGet downloads url into memory, reporting percentage completion through
// onPct when the server sends a Content-Length. Responses larger than max are
// rejected instead of buffered.
func httpGet(ctx context.Context, url string, max int64, onPct func(int)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("nodeinstall: build request for %s: %w", url, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nodeinstall: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nodeinstall: GET %s: unexpected status %s", url, resp.Status)
	}
	if resp.ContentLength > max {
		return nil, fmt.Errorf("nodeinstall: GET %s: response too large (%d bytes)", url, resp.ContentLength)
	}

	var body io.Reader = ctxReader{ctx: ctx, r: resp.Body}
	if onPct != nil && resp.ContentLength > 0 {
		body = &progressReader{r: body, total: resp.ContentLength, fn: onPct}
	}

	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(body, max+1))
	if err != nil {
		return nil, fmt.Errorf("nodeinstall: GET %s: %w", url, err)
	}
	if n > max {
		return nil, fmt.Errorf("nodeinstall: GET %s: response exceeds %d bytes", url, max)
	}
	return buf.Bytes(), nil
}

// ctxReader aborts an in-flight body read once ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// progressReader reports read progress as a percentage of total.
type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	last  int
	fn    func(int)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 {
		pct := int(p.read * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
		if pct != p.last {
			p.last = pct
			p.fn(pct)
		}
	}
	return n, err
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
