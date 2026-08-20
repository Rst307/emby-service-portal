// Package requests owns the media request workflow: searching TMDB, marking
// results against the live Emby library, recording what a portal user asks
// for, and exposing the administrator list.
package requests

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
)

// EmbyLibrary is the narrow Emby surface the service needs: checking whether
// TMDB IDs already exist in the library and, at submission time, confirming an
// item's provider ID matches the requested title.
type EmbyLibrary interface {
	emby.ProviderLibrary
}

// Service orchestrates search, availability marking, and request lifecycle.
type Service struct {
	store *sqlite.Store
	tmdb  *tmdb.Client
	emby  EmbyLibrary
	now   func() time.Time
}

func New(store *sqlite.Store, tmdbClient *tmdb.Client, embyClient EmbyLibrary) *Service {
	if tmdbClient == nil {
		tmdbClient = tmdb.NewClient("")
	}
	return &Service{store: store, tmdb: tmdbClient, emby: embyClient, now: time.Now}
}

// SearchItem is one TMDB result annotated with its live status for the
// requesting account: whether the title is already in the Emby library and
// whether the account already asked for it (and with which outcome).
type SearchItem struct {
	tmdb.Result
	InLibrary        bool
	AlreadyRequested bool
	RequestStatus    string
}

// Search returns TMDB results for the query, marking each against the Emby
// library inventory and the account's previous requests. Results already
// present in the library carry InLibrary; user-owned requests carry their
// current status so the page can reactivate a rejected request.
func (s *Service) Search(ctx context.Context, accountID int64, query string, limit int) ([]SearchItem, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	results, err := s.tmdb.SearchMulti(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	types := []string{"movie", "tv"}
	inLibrary := map[string]bool{}
	if s.emby != nil {
		if found, err := s.emby.AnyProviderIDExists(ctx, types, ids); err == nil {
			inLibrary = found
		} else {
			// A library outage must not break search: results stay visible
			// without the availability mark.
			inLibrary = map[string]bool{}
		}
	}
	existing, err := s.store.ListMediaRequestsForAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load previous requests: %w", err)
	}
	items := make([]SearchItem, 0, len(results))
	for _, result := range results {
		key := fmt.Sprintf("%s:%d", result.MediaType, result.ID)
		item := SearchItem{Result: result, InLibrary: inLibrary[key]}
		if status, ok := existing[key]; ok {
			item.AlreadyRequested = true
			item.RequestStatus = status
		}
		items = append(items, item)
	}
	return items, nil
}

// Create records a request from an authenticated account. The server re-fetches
// the catalog record from TMDB by media type and ID (client-supplied values are
// not trusted), verifies the title is not already in the library, then upserts
// the request row. A previously rejected request is reactivated.
func (s *Service) Create(ctx context.Context, account domain.Account, mediaType string, tmdbID int64) (domain.MediaRequest, error) {
	if s.tmdb == nil || !s.tmdb.Configured() {
		return domain.MediaRequest{}, tmdb.ErrNotConfigured
	}
	details, found, err := s.tmdb.Details(ctx, mediaType, tmdbID)
	if err != nil {
		return domain.MediaRequest{}, err
	}
	if !found {
		return domain.MediaRequest{}, fmt.Errorf("TMDB 中没有找到对应的影视条目")
	}
	if s.emby != nil {
		found, err := s.emby.AnyProviderIDExists(ctx, []string{mediaType}, []int64{tmdbID})
		if err != nil {
			return domain.MediaRequest{}, fmt.Errorf("检查 Emby 库存失败：%w", err)
		}
		if found[fmt.Sprintf("%s:%d", mediaType, tmdbID)] {
			return domain.MediaRequest{}, domain.ErrRequestInLibrary
		}
	}
	input := domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: details.ID, MediaType: details.MediaType,
		Title: details.Title, OriginalTitle: details.OriginalTitle,
		Overview: details.Overview, PosterPath: details.PosterPath,
		ReleaseDate: details.ReleaseDate, Now: s.now(),
	}
	request, err := s.store.UpsertMediaRequest(ctx, input)
	if err != nil {
		return domain.MediaRequest{}, fmt.Errorf("保存求剧记录失败：%w", err)
	}
	return request, nil
}

// List returns a filtered page of requests for the administrator.
func (s *Service) List(ctx context.Context, filter domain.MediaRequestFilter) (domain.MediaRequestPage, error) {
	return s.store.ListMediaRequests(ctx, filter)
}

// SetStatus marks a request fulfilled or rejected.
func (s *Service) SetStatus(ctx context.Context, id int64, status string) error {
	if status != domain.MediaRequestFulfilled && status != domain.MediaRequestRejected {
		return fmt.Errorf("invalid request status %q", status)
	}
	return s.store.SetMediaRequestStatus(ctx, id, status, s.now())
}

// Delete removes a request record.
func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.store.DeleteMediaRequest(ctx, id)
}
