// Package update implements self-update for the portal binary: release
// detection against the GitHub releases feed, checksum-verified download, and
// in-place replacement followed by a process restart.
//
// Replacement requires write access to the executable's directory. On Linux
// the running file can be renamed, so the swap happens in process and the
// process exits with RestartExitCode for the service manager (systemd
// Restart=on-failure) to relaunch the new binary. On Windows the executable
// is locked while running, so a detached helper .bat waits for the process to
// exit, swaps the file and relaunches it. When the directory is not writable
// (e.g. a Docker image or a hardened systemd service) Apply reports a clear
// error instead of half-updating.
package update

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rst307/emby-service-portal/internal/buildinfo"
)

// RestartExitCode is the process exit code used after an applied update. The
// service manager (systemd Restart=on-failure, a supervisor, or a restart
// wrapper) treats it as a restart request; the new binary then serves.
const RestartExitCode = 17

const (
	repoOwner    = "Rst307"
	repoName     = "emby-service-portal"
	userAgent    = "emby-service-portal-updater/1.0"
	autoKey      = "auto_update"
	releasePath  = "/repos/%s/%s/releases?per_page=10"
	downloadPath = "/repos/%s/%s/releases/download/%s/%s"
)

// SettingStore is the settings key/value persistence the updater uses for the
// auto-update flag. *sqlite.Store satisfies it.
type SettingStore interface {
	Setting(ctx context.Context, key string) (value string, ok bool, err error)
	SetSetting(ctx context.Context, key, value string) error
}

// Options configures a Service.
type Options struct {
	// APIBase is the GitHub API root (default https://api.github.com). Point
	// it at a mirror where the public host is slow or blocked.
	APIBase string
	// DownloadBase optionally replaces the asset download root. When set,
	// assets are resolved as DownloadBase/repos/Rst307/emby-service-portal/
	// releases/download/<tag>/<asset> (useful behind a release proxy).
	DownloadBase string
	// AutoDefault seeds the stored auto-update flag on first run.
	AutoDefault bool
	// Interval is how often the background checker runs (default 6h).
	Interval time.Duration
}

// Release describes the newest applicable GitHub release.
type Release struct {
	Version     string
	PublishedAt time.Time
	Notes       string
	AssetName   string
	AssetURL    string
	AssetSize   int64
	Checksum    string
	Prerelease  bool
}

// State is a thread-safe snapshot for the admin UI.
type State struct {
	CurrentVersion string
	Interval       time.Duration
	AutoUpdate     bool // auto-update enabled (stored setting)
	LastCheck      time.Time
	CheckError     string // non-empty when the latest check failed
	Latest         *Release
	Updating       bool // an update is being applied right now
	Applied        bool // update applied; restart pending
}

// Available reports whether a newer applicable release was found.
func (s State) Available() bool {
	return s.Latest != nil && s.ApplyBlocked() == "" && !s.Updating && !s.Applied
}

// ApplyBlocked returns "" when Apply can proceed, otherwise the reason why
// not, in Chinese, for display in the admin UI.
func (s State) ApplyBlocked() string {
	switch {
	case s.Updating:
		return "正在更新中"
	case s.Applied:
		return "更新已应用，正在重启"
	case s.CheckError != "":
		return "最近一次检测失败"
	case s.Latest == nil:
		return "尚无可用更新"
	case s.Latest.Checksum == "":
		return "发布缺少校验和，已阻止自动安装"
	}
	return ""
}

// Service performs release checks and applies updates.
type Service struct {
	store        SettingStore
	client       *http.Client
	apiBase      string
	downloadBase string
	assetName    string // expected asset name for this platform ("" = unsupported)
	autoDefault  bool
	interval     time.Duration

	mu    sync.Mutex
	state State
}

// New returns an update service.
func New(store SettingStore, options Options) *Service {
	apiBase := strings.TrimRight(strings.TrimSpace(options.APIBase), "/")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	interval := options.Interval
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	s := &Service{
		store:        store,
		client:       &http.Client{Timeout: 20 * time.Second},
		apiBase:      apiBase,
		downloadBase: strings.TrimRight(options.DownloadBase, "/"),
		autoDefault:  options.AutoDefault,
		interval:     interval,
		assetName:    assetNameFor(runtime.GOOS, runtime.GOARCH),
	}
	s.state.CurrentVersion = buildinfo.Version
	s.state.Interval = interval
	return s
}

// Interval returns how often the background checker should run.
func (s *Service) Interval() time.Duration { return s.interval }

// Ensure seeds the stored auto-update flag from the env-provided default when
// no value has been persisted yet. Call once at startup.
func (s *Service) Ensure(ctx context.Context) error {
	value := "0"
	if s.autoDefault {
		value = "1"
	}
	_, ok, err := s.store.Setting(ctx, autoKey)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return s.store.SetSetting(ctx, autoKey, value)
}

// Cleanup removes stale artifacts from a previous update (a replaced binary or
// leftover temp download). Call once at startup, before serving.
func (s *Service) Cleanup() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	dir, base := filepath.Split(executable)
	for _, suffix := range []string{".old", ".update.tmp", ".update.bat"} {
		path := filepath.Join(dir, base+suffix)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clean stale update artifact %s: %w", path, err)
		}
	}
	return nil
}

// Auto returns whether auto-update is enabled.
func (s *Service) Auto(ctx context.Context) bool {
	value, ok, err := s.store.Setting(ctx, autoKey)
	if err != nil || !ok {
		return s.autoDefault
	}
	return value == "1"
}

// SetAuto persists the auto-update flag.
func (s *Service) SetAuto(ctx context.Context, enabled bool) error {
	value := "0"
	if enabled {
		value = "1"
	}
	if err := s.store.SetSetting(ctx, autoKey, value); err != nil {
		return fmt.Errorf("保存自动更新设置: %w", err)
	}
	s.mu.Lock()
	s.state.AutoUpdate = enabled
	s.mu.Unlock()
	return nil
}

// Snapshot returns the current state for display.
func (s *Service) Snapshot(ctx context.Context) State {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	state.AutoUpdate = s.Auto(ctx)
	return state
}

// Check queries the release feed and refreshes the cached state.
func (s *Service) Check(ctx context.Context) error {
	release, err := s.fetchLatest(ctx)
	s.mu.Lock()
	s.state.LastCheck = time.Now()
	s.state.CheckError = ""
	s.state.Latest = release
	if err != nil {
		s.state.CheckError = err.Error()
	}
	s.mu.Unlock()
	return err
}

// applySlot reserves the right to apply one update; it reports false when an
// apply is already running or finished.
func (s *Service) applySlot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Updating || s.state.Applied {
		return false
	}
	s.state.Updating = true
	return true
}

func (s *Service) finishApply(applied bool) {
	s.mu.Lock()
	s.state.Updating = false
	s.state.Applied = applied
	s.mu.Unlock()
}

// ResetApplied clears the Applied flag (used by the manager after restart;
// harmless to skip).
func (s *Service) ResetApplied() {
	s.mu.Lock()
	s.state.Applied = false
	s.mu.Unlock()
}

// Apply downloads, verifies and installs the newest release. On success it
// returns (true, nil): the binary on disk is already the new one (Linux) or a
// waiting restart helper was spawned (Windows). The caller must then stop the
// HTTP server and exit with RestartExitCode after flushing its response.
func (s *Service) Apply(ctx context.Context) (bool, error) {
	if !s.applySlot() {
		return false, errors.New("更新正在进行中")
	}
	restart, err := s.apply(ctx)
	s.finishApply(err == nil && restart)
	return restart, err
}

func (s *Service) apply(ctx context.Context) (bool, error) {
	s.mu.Lock()
	latest := s.state.Latest
	s.mu.Unlock()
	if latest == nil {
		if err := s.Check(ctx); err != nil {
			return false, fmt.Errorf("无法获取更新信息: %w", err)
		}
		s.mu.Lock()
		latest = s.state.Latest
		s.mu.Unlock()
	}
	if latest == nil {
		return false, errors.New("没有可用的新版本")
	}
	if compareVersions(latest.Version, buildinfo.Version) == 0 {
		return false, fmt.Errorf("当前已是最新版本 %s", latest.Version)
	}
	downloaded, err := s.downloadAndVerify(ctx, latest)
	if err != nil {
		return false, err
	}
	restart, err := s.install(downloaded)
	if err != nil {
		_ = os.Remove(downloaded)
		return false, err
	}
	return restart, nil
}

// BackgroundTick performs the periodic check. With auto-update enabled it
// applies an available update and returns true so the caller can restart.
// Without auto-update it only refreshes the detection state for the UI.
func (s *Service) BackgroundTick(ctx context.Context) (bool, error) {
	if err := s.Check(ctx); err != nil {
		return false, err
	}
	s.mu.Lock()
	available := s.state.Latest != nil && compareVersions(s.state.Latest.Version, buildinfo.Version) != 0
	s.mu.Unlock()
	if !available || !s.Auto(ctx) {
		return false, nil
	}
	return s.Apply(ctx)
}

// fetchLatest returns the newest non-draft release and the matching asset and
// checksum for this platform. Releases are compared by tag; a pre-release
// counts when it is the newest (this project's CI publishes pre-release
// builds for every merge).
func (s *Service) fetchLatest(ctx context.Context) (*Release, error) {
	url := s.apiBase + fmt.Sprintf(releasePath, repoOwner, repoName)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return nil, fmt.Errorf("更新源返回 %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var releases []struct {
		TagName     string `json:"tag_name"`
		Prerelease  bool   `json:"prerelease"`
		Draft       bool   `json:"draft"`
		PublishedAt string `json:"published_at"`
		Body        string `json:"body"`
		Assets      []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(response.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("解析更新源响应失败: %w", err)
	}
	sort.SliceStable(releases, func(i, j int) bool {
		return compareVersions(releases[i].TagName, releases[j].TagName) > 0
	})
	selected := -1
	for i := range releases {
		if !releases[i].Draft {
			selected = i
			break
		}
	}
	if selected < 0 {
		return nil, errors.New("更新源没有可用的发布")
	}
	newest := releases[selected]
	if s.assetName == "" {
		return nil, fmt.Errorf("当前平台 %s/%s 暂不支持自动更新", runtime.GOOS, runtime.GOARCH)
	}
	result := &Release{
		Version:    newest.TagName,
		Prerelease: newest.Prerelease,
		Notes:      newest.Body,
	}
	if publishedAt, err := time.Parse(time.RFC3339, newest.PublishedAt); err == nil {
		result.PublishedAt = publishedAt
	}
	for _, asset := range newest.Assets {
		if asset.Name != s.assetName {
			continue
		}
		result.AssetName = asset.Name
		result.AssetURL = s.assetURL(newest.TagName, asset.Name, asset.BrowserDownloadURL)
		result.AssetSize = asset.Size
	}
	if result.AssetName == "" {
		return nil, fmt.Errorf("发布 %s 缺少 %s 的安装包", newest.TagName, s.assetName)
	}
	checksum, err := s.fetchChecksum(ctx, newest.TagName, newest.Assets)
	if err != nil {
		return nil, err
	}
	result.Checksum = checksum
	return result, nil
}

// assetURL resolves the download URL: a configured download base (mirror or
// release proxy) wins; otherwise the official browser_download_url from the
// API is used, falling back to a constructed github.com URL.
func (s *Service) assetURL(tag, name, browserURL string) string {
	if s.downloadBase != "" {
		return s.downloadBase + fmt.Sprintf(downloadPath, repoOwner, repoName, tag, name)
	}
	if browserURL != "" {
		return browserURL
	}
	return "https://github.com" + fmt.Sprintf(downloadPath, repoOwner, repoName, tag, name)
}

// fetchChecksum resolves the "<asset>.sha256" sidecar from the release's own
// asset list (so mirrors that rewrite asset URLs keep working) and returns the
// expected hex digest. A missing sidecar blocks auto-install.
func (s *Service) fetchChecksum(ctx context.Context, tag string, assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}) (string, error) {
	checksumName := s.assetName + ".sha256"
	url := ""
	for _, asset := range assets {
		if asset.Name == checksumName {
			url = s.assetURL(tag, asset.Name, asset.BrowserDownloadURL)
			break
		}
	}
	if url == "" {
		return "", fmt.Errorf("发布缺少校验和文件，已阻止自动安装")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("获取校验和失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("发布缺少校验和文件（HTTP %s），已阻止自动安装", response.Status)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("读取校验和失败: %w", err)
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", errors.New("校验和文件格式无效")
	}
	hexValue := strings.ToLower(fields[0])
	for _, r := range hexValue {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return "", errors.New("校验和文件格式无效")
		}
	}
	return hexValue, nil
}

// downloadAndVerify streams the release asset into a temporary file next to
// the executable (same filesystem, so the final rename is atomic) and verifies
// the SHA-256 digest against the release sidecar.
func (s *Service) downloadAndVerify(ctx context.Context, release *Release) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法确定当前程序路径: %w", err)
	}
	dir, base := filepath.Split(executable)
	target := filepath.Join(dir, base+".update.tmp")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.AssetURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("下载更新失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载更新失败：更新源返回 %s", response.Status)
	}
	file, err := os.Create(target)
	if err != nil {
		return "", fmt.Errorf("程序目录不可写（%v），无法自动更新：请为 %s 所在目录授予写权限，或手动替换程序并重启", err, dir)
	}
	cleanup := func() { _ = file.Close(); _ = os.Remove(target) }
	written, err := io.Copy(file, response.Body)
	if err != nil {
		cleanup()
		return "", fmt.Errorf("下载更新失败: %w", err)
	}
	if release.AssetSize > 0 && written != release.AssetSize {
		cleanup()
		return "", fmt.Errorf("下载不完整：收到 %d 字节，预期 %d", written, release.AssetSize)
	}
	if err := verifyChecksum(release.Checksum, file); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", err
	}
	return target, nil
}

// verifyChecksum computes the SHA-256 of r and compares it with the expected
// hex digest in constant time. r is read from its current position.
func verifyChecksum(expectedHex string, r io.Reader) error {
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return errors.New("校验和格式无效")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
		return fmt.Errorf("校验更新文件失败: %w", err)
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), expected) != 1 {
		return errors.New("校验和不匹配，已中止更新（下载文件可能被篡改）")
	}
	return nil
}

// install moves the downloaded file into place and arranges the restart.
// Platform-specific: Linux swaps in process and returns restart=true; Windows
// spawns the detached helper script. See restart_helper_windows.go and
// restart_helper_other.go.
func (s *Service) install(downloaded string) (bool, error) {
	executable, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("无法确定当前程序路径: %w", err)
	}
	dir, base := filepath.Split(executable)
	return installPlatform(s, dir, base, downloaded)
}

// assetNameFor returns the release asset name for the running platform, or ""
// for platforms that publish no asset.
func assetNameFor(goos, goarch string) string {
	switch {
	case goos == "linux" && goarch == "amd64":
		return "emby-service-portal-linux-amd64"
	case goos == "windows" && goarch == "amd64":
		return "emby-service-portal-windows-amd64.exe"
	default:
		return ""
	}
}

// compareVersions orders release tags like v0.0.0-build.<n>. Returns >0 when a
// is newer, <0 when older, 0 when equal. Unparsable tags compare as older than
// any parsable one ("dev" falls in this bucket), so a dev build always sees a
// release as an update.
func compareVersions(a, b string) int {
	na, okA := parseVersion(a)
	nb, okB := parseVersion(b)
	switch {
	case !okA && !okB:
		return strings.Compare(a, b)
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := range na {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// parseVersion parses vMAJOR.MINOR.PATCH[-build.N] into comparable numbers.
func parseVersion(tag string) ([4]int, bool) {
	value := strings.TrimSpace(strings.TrimPrefix(tag, "v"))
	prerelease := ""
	if index := strings.Index(value, "-"); index >= 0 {
		prerelease = value[index+1:]
		value = value[:index]
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return [4]int{}, false
	}
	var result [4]int
	for i, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return [4]int{}, false
		}
		result[i] = number
	}
	if prerelease != "" {
		if !strings.HasPrefix(prerelease, "build.") {
			return [4]int{}, false
		}
		number, err := strconv.Atoi(strings.TrimPrefix(prerelease, "build."))
		if err != nil || number < 0 {
			return [4]int{}, false
		}
		result[3] = number
	}
	return result, true
}
