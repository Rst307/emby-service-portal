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
}

func FromEnv() (Config, error) {
	cookieSecure, err := boolEnv("EUM_COOKIE_SECURE", true)
	if err != nil {
		return Config{}, err
	}
	ttl, err := durationEnv("EUM_SESSION_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ListenAddr:            value("EUM_LISTEN_ADDR", ":8080"),
		DatabasePath:          os.Getenv("EUM_DATABASE_PATH"),
		EmbyBaseURL:           os.Getenv("EUM_EMBY_BASE_URL"),
		EmbyAPIKey:            os.Getenv("EUM_EMBY_API_KEY"),
		APIKey:                os.Getenv("EUM_API_KEY"),
		CredentialMasterKey:   os.Getenv("EUM_CREDENTIAL_MASTER_KEY"),
		CredentialPreviousKey: os.Getenv("EUM_CREDENTIAL_PREVIOUS_MASTER_KEY"),
		AdminUsername:         os.Getenv("EUM_ADMIN_USERNAME"),
		AdminPassword:         os.Getenv("EUM_ADMIN_PASSWORD"),
		CookieSecure:          cookieSecure,
		SessionTTL:            ttl,
		TimeZone:              value("EUM_TIME_ZONE", "Asia/Shanghai"),
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	for name, v := range map[string]string{
		"EUM_DATABASE_PATH": c.DatabasePath, "EUM_EMBY_BASE_URL": c.EmbyBaseURL,
		"EUM_EMBY_API_KEY": c.EmbyAPIKey, "EUM_API_KEY": c.APIKey, "EUM_CREDENTIAL_MASTER_KEY": c.CredentialMasterKey,
		"EUM_ADMIN_USERNAME": c.AdminUsername, "EUM_ADMIN_PASSWORD": c.AdminPassword,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	u, err := url.Parse(c.EmbyBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("EUM_EMBY_BASE_URL must be an absolute HTTP(S) URL")
	}
	if u.Scheme == "http" && !isLocalHost(u.Hostname()) {
		return fmt.Errorf("EUM_EMBY_BASE_URL must use HTTPS outside localhost")
	}
	if len(c.CredentialMasterKey) < 32 {
		return fmt.Errorf("EUM_CREDENTIAL_MASTER_KEY must be at least 32 characters")
	}
	if c.SessionTTL <= 0 {
		return fmt.Errorf("EUM_SESSION_TTL must be positive")
	}
	if _, err := c.TimeLocation(); err != nil {
		return err
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
		return nil, fmt.Errorf("EUM_TIME_ZONE must be a valid IANA time zone: %w", err)
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
