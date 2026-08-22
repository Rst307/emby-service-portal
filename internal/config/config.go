// Package config loads and validates process configuration.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	ListenAddr            string
	DatabasePath          string
	EmbyBaseURL           string
	EmbyAPIKey            string
	APIKey                string
	CredentialMasterKey   string
	CredentialPreviousKey string
	AdminUsername         string
	AdminPassword         string
	CookieSecure          bool
	SessionTTL            time.Duration
	TimeZone              string
	TmdbAPIKey            string
	// TmdbBaseURL overrides the public TMDB API root (default
	// https://api.themoviedb.org/3). Point it at a reachable TMDB mirror or
	// reverse proxy where the public host is slow or blocked (e.g. mainland
	// China). The value must include the /3 path segment the mirror serves.
	TmdbBaseURL string
	// TmdbImageBaseURL overrides the poster CDN root (default
	// https://image.tmdb.org/t/p/w342) used by rendered poster images and the
	// page CSP, for browser networks that cannot reach the public TMDB CDN.
	TmdbImageBaseURL string
	// TmdbHTTPProxy routes TMDB API requests through an explicit HTTP(S) or
	// SOCKS5 proxy instead of the process environment proxy.
	TmdbHTTPProxy string
	// TmdbTimeout bounds each TMDB API request (default 10s).
	TmdbTimeout time.Duration
	// UpdateAPIBase is the GitHub API root used for release detection
	// (default https://api.github.com). Point it at a mirror where the public
	// host is slow or blocked (e.g. mainland China).
	UpdateAPIBase string
	// UpdateDownloadBase optionally replaces the release asset download root;
	// when set, assets resolve as
	// <base>/repos/Rst307/emby-service-portal/releases/download/<tag>/<asset>
	// (useful behind a GitHub release proxy).
	UpdateDownloadBase string
	// UpdateInterval is how often the background check runs (default 6h).
	// The check always only detects; auto-install additionally requires the
	// stored auto-update flag (ESP_UPDATE_AUTO seeds it on first run).
	UpdateInterval time.Duration
	// UpdateAuto seeds the stored auto-update flag on first run (default
	// false). The flag can be toggled at runtime in 系统设置.
	UpdateAuto bool
	// UpdateHTTPProxy routes GitHub release API and asset download requests
	// through an explicit HTTP(S) or SOCKS5 proxy instead of the process
	// environment proxy (useful where api.github.com is slow or blocked,
	// e.g. mainland China).
	UpdateHTTPProxy string
}

// DefaultUpdateInterval is the update check interval used when
// ESP_UPDATE_INTERVAL is unset.
const DefaultUpdateInterval = 6 * time.Hour

func FromEnv() (Config, error) {
	cookieSecure, err := boolEnv("ESP_COOKIE_SECURE", true)
	if err != nil {
		return Config{}, err
	}
	ttl, err := durationEnv("ESP_SESSION_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	tmdbTimeout, err := durationEnv("ESP_TMDB_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	updateAuto, err := boolEnv("ESP_UPDATE_AUTO", false)
	if err != nil {
		return Config{}, err
	}
	updateInterval, err := durationEnv("ESP_UPDATE_INTERVAL", DefaultUpdateInterval)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ListenAddr:            value("ESP_LISTEN_ADDR", ":8080"),
		DatabasePath:          os.Getenv("ESP_DATABASE_PATH"),
		EmbyBaseURL:           os.Getenv("ESP_EMBY_BASE_URL"),
		EmbyAPIKey:            os.Getenv("ESP_EMBY_API_KEY"),
		APIKey:                os.Getenv("ESP_API_KEY"),
		CredentialMasterKey:   os.Getenv("ESP_CREDENTIAL_MASTER_KEY"),
		CredentialPreviousKey: os.Getenv("ESP_CREDENTIAL_PREVIOUS_MASTER_KEY"),
		AdminUsername:         os.Getenv("ESP_ADMIN_USERNAME"),
		AdminPassword:         os.Getenv("ESP_ADMIN_PASSWORD"),
		CookieSecure:          cookieSecure,
		SessionTTL:            ttl,
		TimeZone:              value("ESP_TIME_ZONE", "Asia/Shanghai"),
		TmdbAPIKey:            os.Getenv("ESP_TMDB_API_KEY"),
		TmdbBaseURL:           os.Getenv("ESP_TMDB_BASE_URL"),
		TmdbImageBaseURL:      os.Getenv("ESP_TMDB_IMAGE_BASE_URL"),
		TmdbHTTPProxy:         os.Getenv("ESP_TMDB_HTTP_PROXY"),
		TmdbTimeout:           tmdbTimeout,
		UpdateAPIBase:         value("ESP_UPDATE_API_BASE", "https://api.github.com"),
		UpdateDownloadBase:    os.Getenv("ESP_UPDATE_DOWNLOAD_BASE"),
		UpdateInterval:        updateInterval,
		UpdateAuto:            updateAuto,
		UpdateHTTPProxy:       os.Getenv("ESP_UPDATE_HTTP_PROXY"),
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	for name, v := range map[string]string{
		"ESP_DATABASE_PATH": c.DatabasePath, "ESP_EMBY_BASE_URL": c.EmbyBaseURL,
		"ESP_EMBY_API_KEY": c.EmbyAPIKey, "ESP_API_KEY": c.APIKey, "ESP_CREDENTIAL_MASTER_KEY": c.CredentialMasterKey,
		"ESP_ADMIN_USERNAME": c.AdminUsername, "ESP_ADMIN_PASSWORD": c.AdminPassword,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	u, err := url.Parse(c.EmbyBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("ESP_EMBY_BASE_URL must be an absolute HTTP(S) URL")
	}
	if u.Scheme == "http" && !isLocalHost(u.Hostname()) {
		return fmt.Errorf("ESP_EMBY_BASE_URL must use HTTPS outside localhost")
	}
	if len(c.CredentialMasterKey) < 32 {
		return fmt.Errorf("ESP_CREDENTIAL_MASTER_KEY must be at least 32 characters")
	}
	if c.SessionTTL <= 0 {
		return fmt.Errorf("ESP_SESSION_TTL must be positive")
	}
	for name, val := range map[string]string{
		"ESP_TMDB_BASE_URL":       c.TmdbBaseURL,
		"ESP_TMDB_IMAGE_BASE_URL": c.TmdbImageBaseURL,
		"ESP_TMDB_HTTP_PROXY":     c.TmdbHTTPProxy,
		"ESP_UPDATE_HTTP_PROXY":   c.UpdateHTTPProxy,
	} {
		if val == "" {
			continue
		}
		schemes := []string{"http", "https"}
		if name == "ESP_TMDB_HTTP_PROXY" || name == "ESP_UPDATE_HTTP_PROXY" {
			schemes = []string{"http", "https", "socks5", "socks5h"}
		}
		u, err := url.Parse(val)
		if err != nil || u.Host == "" {
			return fmt.Errorf("%s must be an absolute URL", name)
		}
		allowed := false
		for _, scheme := range schemes {
			if strings.EqualFold(u.Scheme, scheme) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%s must use one of %v", name, schemes)
		}
	}
	if _, err := c.TimeLocation(); err != nil {
		return err
	}
	for name, val := range map[string]string{
		"ESP_UPDATE_API_BASE":      c.UpdateAPIBase,
		"ESP_UPDATE_DOWNLOAD_BASE": c.UpdateDownloadBase,
	} {
		if strings.TrimSpace(val) == "" {
			continue
		}
		u, err := url.Parse(val)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("%s must be an absolute HTTP(S) URL", name)
		}
	}
	return nil
}

// TimeLocation returns the configured IANA time zone. An empty value retains
// the historical UTC behavior for programmatic configuration.
func (c Config) TimeLocation() (*time.Location, error) {
	name := strings.TrimSpace(c.TimeZone)
	if name == "" {
		name = "UTC"
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("ESP_TIME_ZONE must be a valid IANA time zone: %w", err)
	}
	return location, nil
}

func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func boolEnv(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false: %w", key, err)
	}
	return parsed, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(v)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}
