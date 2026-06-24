// fetch.go implements the real Fetcher and Spawner: the side-effecting halves of
// managed mode. Fetcher resolves the llama-server binary and GGUF weights to
// cached paths under ~/.cache/kitsoki (env-overridable), downloading and
// sha256-verifying them against baked pins on first use. Spawner shells out via
// os/exec. Both are swapped for fakes in tests; nothing here runs in a unit test.
//
// The llama.cpp Linux/macOS releases ship as a tar archive of the llama-server
// binary plus its shared libraries (libggml*, libllama*), so EnsureBinary
// extracts the whole archive into one per-release cache directory and returns the
// path to llama-server inside it; that directory is what the Sidecar puts on
// LD_LIBRARY_PATH. On older-glibc Linux (RHEL/Rocky 9 ship libstdc++ capped at
// GLIBCXX_3.4.29, but the upstream build needs 3.4.30) EnsureBinary also fetches
// a compatible libstdc++ into the same directory, so the zero-touch promise holds
// without any manual system install. See [project memory: local-model-agent].

package server

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
)

// cacheEnv is the environment variable that overrides the cache root. When unset
// the cache lives under the user cache dir (~/.cache/kitsoki on Linux). Operators
// and CI set this to a writable, pre-warmed location.
const cacheEnv = "KITSOKI_CACHE_DIR"

// pin records the download URL and expected sha256 for one cached artifact. The
// hashes are the trust anchor: a fetched file whose sha256 differs from the pin
// is rejected before use, so a compromised or truncated download can never reach
// the model runtime.
type pin struct {
	url    string
	sha256 string
}

// binPins maps a platform key (GOOS/GOARCH) to the llama-server release archive
// to fetch. The URL points at a tar archive (.tar.gz) containing llama-server
// plus its shared libraries; EnsureBinary extracts the whole archive. The sha256
// is over the archive as downloaded.
//
// Pinned to llama.cpp release b9444 (the build verified end-to-end against
// Qwen2.5-1.5B on both a Linux CPU box and Apple Silicon). The darwin/arm64
// archive ships Metal-enabled dylibs that resolve via @loader_path, so the same
// flat-extraction layout that LD_LIBRARY_PATH needs on Linux also lets the
// binary find its libraries on macOS with no extra env (see ldLibraryPathEnv).
var binPins = map[string]pin{
	"linux/amd64": {
		url:    "https://github.com/ggml-org/llama.cpp/releases/download/b9444/llama-b9444-bin-ubuntu-x64.tar.gz",
		sha256: "5d676e97ca82353256f24ed381be60c71f721b4ab52962483e5fce22ea8b3fd4",
	},
	"darwin/arm64": {
		url:    "https://github.com/ggml-org/llama.cpp/releases/download/b9444/llama-b9444-bin-macos-arm64.tar.gz",
		sha256: "36cd51637cf480220eb29b0adf2003b25e4d6e1665920c29634b49355414e8b8",
	},
}

// modelPins maps a model id to the GGUF weights to fetch. Default is the small
// routing/decide model (Qwen2.5-1.5B-Instruct, Apache-2.0).
//
// Note: the upstream Qwen GGUF repo publishes no per-file checksum, so this
// sha256 was computed from the downloaded file and pinned here as the trust
// anchor for all subsequent fetches.
var modelPins = map[string]pin{
	"qwen2.5-1.5b-instruct": {
		url:    "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf",
		sha256: "6a1a2eb6d15622bf3c96857206351ba97e1af16c30d7a74ee38970e434e9407e",
	},
	"nomic-embed-text-v1.5": {
		// sha256 must be computed from the downloaded file (HuggingFace publishes
		// no per-file checksum). Run: sha256sum nomic-embed-text-v1.5.Q4_K_M.gguf
		// and paste the result here to enable managed mode. Until then, use
		// endpoint: mode (point at a running llama-server with --embeddings).
		url:    "https://huggingface.co/nomic-ai/nomic-embed-text-v1.5-GGUF/resolve/main/nomic-embed-text-v1.5.Q4_K_M.gguf",
		sha256: "d4e388894e09cf3816e8b0896d81d265b55e7a9fff9ab03fe8bf4ef5e11295ac",
	},
	"bge-small-en-v1.5": {
		url:    "https://huggingface.co/BAAI/bge-small-en-v1.5/resolve/main/gguf/bge-small-en-v1.5-q8_0.gguf",
		sha256: "", // TODO: fill in after first download
	},
}

// cxxShim describes the libstdc++ runtime fetched on older-glibc Linux. member is
// the path inside the (.tar.bz2) archive to extract and install as libstdc++.so.6
// alongside the binary. conda-forge libstdcxx-ng 12.2.0 provides GLIBCXX_3.4.30,
// which is what the pinned llama.cpp build requires.
type cxxShim struct {
	pin
	member string
}

// cxxPins maps a platform key to the libstdc++ shim used when the OS libstdc++ is
// too old (see requiredGLIBCXXMinor). Only Linux needs this; macOS links its own
// C++ runtime.
var cxxPins = map[string]cxxShim{
	"linux/amd64": {
		pin: pin{
			url:    "https://conda.anaconda.org/conda-forge/linux-64/libstdcxx-ng-12.2.0-h46fd767_19.tar.bz2",
			sha256: "0289e6a7b9a5249161a3967909e12dcfb4ab4475cdede984635d3fb65c606f08",
		},
		member: "lib/libstdc++.so.6.0.30",
	},
}

// requiredGLIBCXXMinor is the minimum GLIBCXX_3.4.<minor> the pinned llama.cpp
// build needs (b9444 was built with GCC 12 → GLIBCXX_3.4.30). When the OS
// libstdc++ tops out below this, EnsureBinary installs the cxxPins shim.
const requiredGLIBCXXMinor = 30

// hostPlatform returns the GOOS/GOARCH key used to select a binary pin.
func hostPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// DefaultModel is the model id provisioned when a prewarm/offline target names
// no specific model. It mirrors the proposal's clean-license default
// (Qwen2.5-1.5B-Instruct, Apache-2.0); see docs/architecture/agent-plugin.md.
const DefaultModel = "qwen2.5-1.5b-instruct"

// PrewarmBinary fetches and verifies the host's llama-server binary into the
// cache without spawning it. It is the entry point behind `make
// fetch-llama-server`: the same fetch-and-verify path managed mode uses on first
// Ask, run ahead of time for offline/CI boxes. It never starts a server and
// never touches the network in endpoint mode (endpoint mode has no binary to
// fetch). Returns the cached path.
func PrewarmBinary(ctx context.Context) (string, error) {
	return NewFetcher().EnsureBinary(ctx, hostPlatform())
}

// PrewarmModel fetches and verifies model's GGUF weights into the cache without
// spawning anything. It is the entry point behind `make fetch-models`. An empty
// model resolves to [DefaultModel]. Returns the cached path.
func PrewarmModel(ctx context.Context, model string) (string, error) {
	if model == "" {
		model = DefaultModel
	}
	return NewFetcher().EnsureModel(ctx, model)
}

// realFetcher provisions artifacts into the on-disk cache. It is the production
// Fetcher; tests substitute a fake via WithFetcher.
type realFetcher struct{}

// NewFetcher returns the production Fetcher backed by the on-disk cache.
func NewFetcher() Fetcher { return &realFetcher{} }

// EnsureBinary returns the cached llama-server path for platform, downloading and
// extracting the release archive on first use. All files in the archive (the
// binary and its shared libraries) land in one per-release directory; on
// older-glibc Linux a compatible libstdc++ is added to the same directory. The
// returned path's directory is what the Sidecar puts on LD_LIBRARY_PATH.
func (f *realFetcher) EnsureBinary(ctx context.Context, platform string) (string, error) {
	p, ok := binPins[platform]
	if !ok {
		return "", fmt.Errorf("no llama-server pin for platform %q", platform)
	}
	if p.url == "" || p.sha256 == "" {
		return "", fmt.Errorf("llama-server (%s): download pin not configured (managed mode unavailable until pins are set)", platform)
	}

	root, err := cacheSubdir("bin")
	if err != nil {
		return "", err
	}
	// One directory per pinned release; its presence (created atomically below)
	// means a verified, fully-extracted archive — so it doubles as the cache hit.
	dir := filepath.Join(root, "llama-"+p.sha256[:12])
	binPath := filepath.Join(dir, "llama-server")

	if _, statErr := os.Stat(binPath); statErr != nil {
		if err := f.fetchAndExtractBinary(ctx, p, platform, dir); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod %s: %w", binPath, err)
	}

	// Older-glibc Linux: ensure a compatible libstdc++ sits next to the binary.
	if err := f.ensureCxxShim(ctx, platform, dir); err != nil {
		return "", err
	}
	return binPath, nil
}

// fetchAndExtractBinary downloads+verifies the release archive and extracts it
// into dir. Extraction is staged in a sibling temp directory and renamed into
// place so a partial extraction never appears as a cache hit.
func (f *realFetcher) fetchAndExtractBinary(ctx context.Context, p pin, platform, dir string) error {
	slog.Info("downloading llama-server ("+platform+")", "url", p.url, "dest", dir)

	tmpArchive, err := downloadVerifiedTemp(ctx, p.url, p.sha256, filepath.Dir(dir), "llama-server")
	if err != nil {
		return fmt.Errorf("llama-server (%s): %w", platform, err)
	}
	defer os.Remove(tmpArchive)

	stage, err := os.MkdirTemp(filepath.Dir(dir), "llama-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	if err := extractTarGzFlat(tmpArchive, stage); err != nil {
		return fmt.Errorf("extract llama-server archive: %w", err)
	}
	if _, statErr := os.Stat(filepath.Join(stage, "llama-server")); statErr != nil {
		return fmt.Errorf("llama-server not found in archive %s", p.url)
	}
	if err := os.Rename(stage, dir); err != nil {
		// A racing fetcher may have created dir first; that is fine — the content
		// is identical (same pinned sha), so treat an existing dir as success.
		if _, statErr := os.Stat(filepath.Join(dir, "llama-server")); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// ensureCxxShim installs a libstdc++ providing GLIBCXX_3.4.>=requiredGLIBCXXMinor
// into dir, but only when the platform has a shim pin AND the OS libstdc++ is too
// old. On a sufficiently new system it is a no-op, so the system runtime is used
// untouched. The shim is named libstdc++.so.6 so LD_LIBRARY_PATH=dir resolves it
// ahead of the system copy.
func (f *realFetcher) ensureCxxShim(ctx context.Context, platform, dir string) error {
	shim, ok := cxxPins[platform]
	if !ok {
		return nil // platform links its own C++ runtime (macOS) or has no pin.
	}
	if maxSystemGLIBCXXMinor() >= requiredGLIBCXXMinor {
		return nil // system libstdc++ is new enough.
	}
	dest := filepath.Join(dir, "libstdc++.so.6")
	if sum, err := fileSHA256(dest); err == nil && sum != "" {
		return nil // already installed (any non-empty hash means present).
	}

	slog.Info("downloading libstdc++ runtime (older-glibc host)", "url", shim.url, "dest", dest)

	tmpArchive, err := downloadVerifiedTemp(ctx, shim.url, shim.sha256, dir, "libstdcxx")
	if err != nil {
		return fmt.Errorf("libstdc++ shim: %w", err)
	}
	defer os.Remove(tmpArchive)

	if err := extractTarBz2Member(tmpArchive, shim.member, dest); err != nil {
		return fmt.Errorf("libstdc++ shim: %w", err)
	}
	return nil
}

// EnsureModel returns the cached GGUF path for model, downloading and verifying
// it on first use.
func (f *realFetcher) EnsureModel(ctx context.Context, model string) (string, error) {
	p, ok := modelPins[model]
	if !ok {
		return "", fmt.Errorf("no weights pin for model %q", model)
	}
	dir, err := cacheSubdir("models")
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, model+".gguf")
	if err := f.ensure(ctx, dest, p, fmt.Sprintf("%s weights", model)); err != nil {
		return "", err
	}
	return dest, nil
}

// ensure makes dest exist with content matching p.sha256: if a cached file is
// already present and verifies, it is reused; otherwise the file is downloaded,
// verified, and only then committed. A pin with an empty URL/sha is a not-yet-
// configured artifact and fails loudly rather than fetching something unverified.
func (f *realFetcher) ensure(ctx context.Context, dest string, p pin, label string) error {
	if p.url == "" || p.sha256 == "" {
		return fmt.Errorf("%s: download pin not configured (managed mode unavailable until pins are set)", label)
	}
	if sum, err := fileSHA256(dest); err == nil && sum == p.sha256 {
		return nil // cached and verified
	}

	// First fetch: announce it (task 3.3) so a multi-GB download is not silent.
	slog.Info("downloading "+label, "url", p.url, "dest", dest)

	if err := downloadVerified(ctx, p.url, p.sha256, dest); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// downloadVerified streams url to a temp file alongside dest, computing the
// sha256 as it goes, and renames it into place only if the hash matches the pin.
// Verifying before the rename means a partial or tampered download never appears
// as a valid cached artifact.
func downloadVerified(ctx context.Context, url, wantSHA, dest string) error {
	tmp, err := downloadVerifiedTemp(ctx, url, wantSHA, filepath.Dir(dest), filepath.Base(dest))
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// downloadVerifiedTemp streams url into a temp file under dir, verifies its sha256
// against wantSHA, and returns the temp file path (the caller renames or extracts
// it, then removes it). A mismatch removes the temp file and errors, so a partial
// or tampered download never escapes this function.
func downloadVerifiedTemp(ctx context.Context, url, wantSHA, dir, prefix string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: http %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, prefix+".part-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		_ = tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA {
		os.Remove(tmpName)
		return "", fmt.Errorf("sha256 mismatch: got %s want %s", got, wantSHA)
	}
	return tmpName, nil
}

// extractTarGzFlat extracts every regular file from the gzip-compressed tar at
// archivePath into destDir, flattening paths to their base name. The llama.cpp
// archives nest all files under a single top-level directory; flattening yields
// the binary and its shared libraries side by side, which is what
// LD_LIBRARY_PATH needs.
func extractTarGzFlat(archivePath, destDir string) error {
	fh, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}
	defer gz.Close()
	return extractTarFlat(tar.NewReader(gz), destDir)
}

// extractTarFlat writes each regular file and symlink in the tar to destDir under
// its base name. Symlinks are preserved with their target also flattened to a
// base name — the llama.cpp archive ships SONAME symlinks
// (libllama-common.so.0 -> libllama-common.so.0.0.NNNN) the binary links against,
// so dropping them leaves it unable to load its own libraries. Flattening keeps
// link and target in the same directory, so the link still resolves.
func extractTarFlat(tr *tar.Reader, destDir string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Base(hdr.Name)
		if name == "" || name == "." || name == ".." {
			continue
		}
		out := filepath.Join(destDir, name)

		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			// Flatten the target to its base name (same flat dir) and recreate the
			// symlink. Remove any stale entry first so extraction is idempotent.
			_ = os.Remove(out)
			if err := os.Symlink(filepath.Base(hdr.Linkname), out); err != nil {
				return err
			}
		case tar.TypeReg:
			w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				_ = w.Close()
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
		default:
			// Directories and other entry types carry no payload we need in the
			// flattened layout.
			continue
		}
	}
}

// extractTarBz2Member extracts a single named entry from the bzip2-compressed tar
// at archivePath and writes it to dest. Used for the libstdc++ shim, whose
// conda-forge package is a .tar.bz2. bzip2 is decompressed via the standard
// library (no external `bzip2` binary, which RHEL/Rocky minimal images omit).
func extractTarBz2Member(archivePath, member, dest string) error {
	fh, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer fh.Close()
	tr := tar.NewReader(bzip2.NewReader(fh))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("member %q not found in archive", member)
		}
		if err != nil {
			return err
		}
		if filepath.Clean(hdr.Name) != filepath.Clean(member) {
			continue
		}
		tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		if _, err := io.Copy(tmp, tr); err != nil {
			_ = tmp.Close()
			os.Remove(tmpName)
			return err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return err
		}
		return os.Rename(tmpName, dest)
	}
}

// glibcxxRe matches the versioned GLIBCXX symbol strings embedded in a
// libstdc++.so.6, e.g. "GLIBCXX_3.4.30".
var glibcxxRe = regexp.MustCompile(`GLIBCXX_3\.4\.([0-9]+)`)

// systemLibstdcxxPaths are where a system libstdc++.so.6 typically lives. The
// first that exists is scanned for its highest GLIBCXX_3.4.<minor> symbol.
var systemLibstdcxxPaths = []string{
	"/lib64/libstdc++.so.6",
	"/usr/lib64/libstdc++.so.6",
	"/usr/lib/x86_64-linux-gnu/libstdc++.so.6",
	"/usr/lib/libstdc++.so.6",
}

// maxSystemGLIBCXXMinor returns the highest GLIBCXX_3.4.<minor> the system
// libstdc++ provides, or -1 if none can be found/read. -1 (treated as "too old")
// makes ensureCxxShim install the shim rather than risk a fail-to-launch.
func maxSystemGLIBCXXMinor() int {
	for _, p := range systemLibstdcxxPaths {
		// Resolve symlinks so we read the real .so payload.
		real := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			real = r
		}
		data, err := os.ReadFile(real)
		if err != nil {
			continue
		}
		return maxGLIBCXXMinorInBytes(data)
	}
	return -1
}

// maxGLIBCXXMinorInBytes returns the highest <minor> across all GLIBCXX_3.4.<minor>
// version strings in data, or -1 if none are present. Split out from
// maxSystemGLIBCXXMinor so the parse is unit-testable without a real .so on disk.
func maxGLIBCXXMinorInBytes(data []byte) int {
	best := -1
	for _, m := range glibcxxRe.FindAllSubmatch(data, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > best {
			best = n
		}
	}
	return best
}

// fileSHA256 returns the hex sha256 of the file at path, or an error if it does
// not exist or cannot be read.
func fileSHA256(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// cacheSubdir returns (creating if needed) a subdirectory of the kitsoki cache
// root. The root is KITSOKI_CACHE_DIR when set, else <user-cache-dir>/kitsoki.
func cacheSubdir(name string) (string, error) {
	root := os.Getenv(cacheEnv)
	if root == "" {
		ucd, err := os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("resolve user cache dir: %w", err)
		}
		root = filepath.Join(ucd, "kitsoki")
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache dir %s: %w", dir, err)
	}
	return dir, nil
}

// realSpawner launches a process via os/exec. It is the production Spawner; tests
// substitute a fake via WithSpawner.
type realSpawner struct{}

// NewSpawner returns the production Spawner backed by os/exec.
func NewSpawner() Spawner { return &realSpawner{} }

// Start launches bin with args and extra environment (e.g. LD_LIBRARY_PATH for
// the bundled shared libraries). The process outlives ctx (ctx governs only the
// launch); termination is the Sidecar's job via Process.Signal.
func (sp *realSpawner) Start(ctx context.Context, bin string, args, env []string) (Process, error) {
	cmd := exec.Command(bin, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}

// execProcess adapts *exec.Cmd to the Process interface.
type execProcess struct{ cmd *exec.Cmd }

// Signal delivers sig to the underlying OS process.
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }

// Wait reaps the process and returns its exit error.
func (p *execProcess) Wait() error { return p.cmd.Wait() }

// ldLibraryPathEnv builds the LD_LIBRARY_PATH entry that puts libDir (the
// extracted binary's directory, holding libggml*/libllama* and any libstdc++
// shim) ahead of the existing search path. Returns nil on non-Linux, where the
// archive's libraries are resolved via @rpath/install_name instead.
func ldLibraryPathEnv(libDir string) []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	v := libDir
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		v += ":" + existing
	}
	return []string{"LD_LIBRARY_PATH=" + v}
}
