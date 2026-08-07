package settings

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emby-user-manager/emby-user-manager/internal/persistence/sqlite"
)

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestEnsureSeedsDefaultWhenAbsent(t *testing.T) {
	service := New(openStore(t), mustLocation(t, "Asia/Shanghai"))
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	name, location := service.DisplayTimeZone(context.Background())
	if name != "Asia/Shanghai" || location.String() != "Asia/Shanghai" {
		t.Fatalf("DisplayTimeZone = %q/%v, want Asia/Shanghai", name, location)
	}
}

func TestEnsureKeepsExistingSetting(t *testing.T) {
	store := openStore(t)
	service := New(store, mustLocation(t, "Asia/Shanghai"))
	if err := service.SetDisplayTimeZone(context.Background(), "America/New_York"); err != nil {
		t.Fatal(err)
	}
	if err := service.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	name, _ := service.DisplayTimeZone(context.Background())
	if name != "America/New_York" {
		t.Fatalf("DisplayTimeZone = %q, want America/New_York", name)
	}
}

func TestSetDisplayTimeZoneRejectsInvalidZone(t *testing.T) {
	service := New(openStore(t), mustLocation(t, "UTC"))
	err := service.SetDisplayTimeZone(context.Background(), "not/a-time-zone")
	if err == nil || !strings.Contains(err.Error(), "invalid IANA time zone") {
		t.Fatalf("SetDisplayTimeZone error = %v, want invalid zone error", err)
	}
	err = service.SetDisplayTimeZone(context.Background(), "   ")
	if err == nil {
		t.Fatal("SetDisplayTimeZone accepted empty name")
	}
}

func TestDisplayTimeZoneFallsBackWhenSettingInvalid(t *testing.T) {
	store := openStore(t)
	if err := store.SetSetting(context.Background(), DisplayTimeZoneKey, "broken/zone"); err != nil {
		t.Fatal(err)
	}
	service := New(store, mustLocation(t, "UTC"))
	name, location := service.DisplayTimeZone(context.Background())
	if name != "UTC" || location != time.UTC {
		t.Fatalf("DisplayTimeZone = %q/%v, want fallback UTC", name, location)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
