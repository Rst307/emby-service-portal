// Package settings provides runtime-configurable application settings.
package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

// DisplayTimeZoneKey is the settings key holding the display time zone.
const DisplayTimeZoneKey = "display_timezone"

type Service struct {
	store           *sqlite.Store
	defaultName     string
	defaultLocation *time.Location
}

// New returns a settings service whose fallback time zone is defaultLocation
// (used until the setting is seeded and whenever the stored value is invalid).
func New(store *sqlite.Store, defaultLocation *time.Location) *Service {
	if defaultLocation == nil {
		defaultLocation = time.UTC
	}
	return &Service{store: store, defaultName: defaultLocation.String(), defaultLocation: defaultLocation}
}

// Ensure seeds the display time zone setting with the default when absent.
func (s *Service) Ensure(ctx context.Context) error {
	if _, ok, err := s.store.Setting(ctx, DisplayTimeZoneKey); err != nil {
		return err
	} else if ok {
		return nil
	}
	return s.store.SetSetting(ctx, DisplayTimeZoneKey, s.defaultName)
}

// DisplayTimeZone returns the configured zone name and its parsed location.
// An absent or unparsable stored value falls back to the default zone.
func (s *Service) DisplayTimeZone(ctx context.Context) (string, *time.Location) {
	name, ok, err := s.store.Setting(ctx, DisplayTimeZoneKey)
	if err != nil || !ok {
		return s.defaultName, s.defaultLocation
	}
	if location, err := time.LoadLocation(name); err == nil {
		return name, location
	}
	return s.defaultName, s.defaultLocation
}

// SetDisplayTimeZone validates name as an IANA zone and persists it.
func (s *Service) SetDisplayTimeZone(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("time zone is required")
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("invalid IANA time zone %q", name)
	}
	return s.store.SetSetting(ctx, DisplayTimeZoneKey, name)
}
