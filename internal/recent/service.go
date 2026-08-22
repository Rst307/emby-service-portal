// Package recent monitors Emby library additions: it records the newest items
// for the portal 最近更新 feed and auto-fulfills pending media requests whose
// TMDB title shows up among the additions, closing the 求剧 loop without
// administrator intervention.
package recent

import (
	"context"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

// ScanBatch is how many newest library items one scan inspects. Emby returns
// them sorted by DateCreated descending, so the batch covers every recent
// addition between scans.
const ScanBatch = 30

// KeepLatest is how many recorded additions the portal feed retains; older
// entries are pruned on every scan.
const KeepLatest = 50

// Service records library additions and serves the 最近更新 feed.
type Service struct {
	store *sqlite.Store
	emby  emby.LibraryWatcher
	// imageBase is the Emby root used to build poster URLs for the feed
	// (e.g. "https://emby.example.com/emby").
	imageBase string
	now       func() time.Time
}

func New(store *sqlite.Store, client emby.LibraryWatcher, embyBaseURL string) *Service {
	return &Service{store: store, emby: client, imageBase: strings.TrimRight(embyBaseURL, "/"), now: time.Now}
}

// ScanOnce fetches the newest library additions, records them, and fulfills
// any pending 求剧 whose TMDB title appears among them. Failures are returned
// for the caller to log; a failed scan never corrupts the stored feed.
func (s *Service) ScanOnce(ctx context.Context) error {
	items, err := s.emby.RecentlyAdded(ctx, ScanBatch)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	for _, item := range items {
		if item.ID == "" {
			continue
		}
		created := item.DateCreated
		if created.IsZero() {
			// Emby gave no parseable timestamp; anchor the entry to the scan
			// so ordering and pruning still behave.
			created = now
		}
		requestID, err := s.store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
			EmbyItemID: item.ID, TmdbID: item.TmdbID, MediaType: item.Type, Title: item.Name,
			DateCreated: created, Now: now,
		})
		if err != nil {
			return err
		}
		if requestID > 0 {
			log.Printf("library watch: %q (tmdb %d) matched a pending request #%d, marked 已入库", item.Name, item.TmdbID, requestID)
		}
	}
	return s.store.PruneRecentlyAdded(ctx, KeepLatest, now)
}

// ItemView pairs one recorded addition with the browser-renderable poster URL
// the portal template uses.
type ItemView struct {
	domain.RecentlyAdded
	ImageURL string
}

// Recent returns the newest recorded additions, newest first, with poster
// URLs resolved against the configured Emby root.
func (s *Service) Recent(ctx context.Context, limit int) ([]ItemView, error) {
	items, err := s.store.ListRecentlyAdded(ctx, limit)
	if err != nil {
		return nil, err
	}
	views := make([]ItemView, 0, len(items))
	for _, item := range items {
		views = append(views, ItemView{RecentlyAdded: item, ImageURL: posterURL(s.imageBase, item.EmbyItemID)})
	}
	return views, nil
}

// posterURL builds the Emby primary-image URL for one item. The image is
// loaded by the browser anonymously, so it must not carry the API key; the
// page CSP allow-lists the Emby origin instead.
func posterURL(imageBase, itemID string) string {
	if imageBase == "" || itemID == "" {
		return ""
	}
	return imageBase + "/Items/" + url.PathEscape(itemID) + "/Images/Primary?maxHeight=438&maxWidth=292"
}
