package config

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		ListenAddr: ":8080", DatabasePath: "manager.db", EmbyBaseURL: "https://emby.example.test/emby",
		EmbyAPIKey: "emby-key", APIKey: "api-key", CredentialMasterKey: "credential-master-key-with-32-characters",
		AdminUsername: "admin", AdminPassword: "password", SessionTTL: time.Hour,
	}
}

func TestValidateRejectsRemotePlaintextEmbyURL(t *testing.T) {
	cfg := validConfig()
	cfg.EmbyBaseURL = "http://emby.example.test/emby"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Validate() error = %v, want HTTPS error", err)
	}
}

func TestValidateAllowsLoopbackHTTPForDevelopment(t *testing.T) {
	cfg := validConfig()
	cfg.EmbyBaseURL = "http://127.0.0.1:8096/emby"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsIanaTimeZone(t *testing.T) {
	cfg := validConfig()
	cfg.TimeZone = "Asia/Shanghai"
	location, err := cfg.TimeLocation()
	if err != nil {
		t.Fatalf("TimeLocation() error = %v", err)
	}
	if location.String() != "Asia/Shanghai" {
		t.Fatalf("location = %q, want Asia/Shanghai", location)
	}
}

func TestValidateRejectsInvalidTimeZone(t *testing.T) {
	cfg := validConfig()
	cfg.TimeZone = "not/a-time-zone"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "EUM_TIME_ZONE") {
		t.Fatalf("Validate() error = %v, want time-zone error", err)
	}
}

func TestValidateRequiresSeparateCredentialMasterKey(t *testing.T) {
	cfg := validConfig()
	cfg.CredentialMasterKey = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "EUM_CREDENTIAL_MASTER_KEY") {
		t.Fatalf("Validate() error = %v, want master key error", err)
	}
}
