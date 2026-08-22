// Package requests owns the media request workflow: searching TMDB, marking
// results against the live Emby library, recording what a portal user asks
// for, and exposing the administrator list.
package requests

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
)

// EmbyLibrary is the narrow Emby surface the service needs: checking whether
// TMDB IDs already exist in the library and, at submission time, confirming an
// item's provider ID matches the requested title, plus the episode footprint of
// a series that already exists (for 缺集/催更 detection).
type EmbyLibrary interface {
	emby.ProviderLibrary
	emby.EpisodeLibrary
}

// ErrTitleNotFound reports that TMDB has no matching catalog entry for the
// requested tmdb_id/media_type combination.
var ErrTitleNotFound = errors.New("TMDB 中没有找到对应的影视条目")

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
	// MissingEpisodes is a short summary like "缺 5 集" when the series already
	// exists in Emby but collected episodes are fewer than the aired TMDB
	// catalog. Empty when the series is complete or unknown.
	MissingEpisodes string
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
		if item.InLibrary && result.MediaType == tmdb.MediaTypeTV {
			// A series already in Emby may still be incomplete: surface how many
			// aired episodes are missing so the card can offer 催更 instead of a
			// blocked 已在库 mark. Detection failures fall back to the plain mark.
			if missing, err := s.missingEpisodeCount(ctx, result.ID); err == nil && missing > 0 {
				item.MissingEpisodes = fmt.Sprintf("缺 %d 集", missing)
			}
		}
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
// Create records a media request for the given account and TMDB title. The
// server re-fetches catalog details from TMDB (never trusting client values)
// and re-checks the Emby library: a movie already in the library is rejected,
// a series already present may become a 催更 (missing-episodes) request.
// Requests aggregate per title — a new requester joins the existing row.
func (s *Service) Create(ctx context.Context, account domain.Account, mediaType string, tmdbID int64) (domain.MediaRequest, error) {
	if s.tmdb == nil || !s.tmdb.Configured() {
		return domain.MediaRequest{}, tmdb.ErrNotConfigured
	}
	details, found, err := s.tmdb.Details(ctx, mediaType, tmdbID)
	if err != nil {
		return domain.MediaRequest{}, err
	}
	if !found {
		return domain.MediaRequest{}, ErrTitleNotFound
	}
	input := domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: details.ID, MediaType: details.MediaType,
		Title: details.Title, OriginalTitle: details.OriginalTitle,
		Overview: details.Overview, PosterPath: details.PosterPath,
		ReleaseDate: details.ReleaseDate, Kind: domain.MediaRequestKindFull, Now: s.now(),
	}
	if s.emby != nil {
		found, err := s.emby.AnyProviderIDExists(ctx, []string{mediaType}, []int64{tmdbID})
		if err != nil {
			return domain.MediaRequest{}, fmt.Errorf("检查 Emby 库存失败：%w", err)
		}
		key := fmt.Sprintf("%s:%d", mediaType, tmdbID)
		if found[key] {
			// A movie already in Emby cannot be requested. A series already in
			// Emby may still be requested as a 催更 (nudge) when episodes are
			// missing; a complete one is rejected like a movie.
			if mediaType == tmdb.MediaTypeTV {
				missing, err := s.missingEpisodeDetails(ctx, tmdbID)
				if err != nil {
					return domain.MediaRequest{}, fmt.Errorf("检查缺失剧集失败：%w", err)
				}
				if len(missing) == 0 {
					return domain.MediaRequest{}, domain.ErrRequestInLibrary
				}
				input.Kind = domain.MediaRequestKindMissing
				input.Episodes = formatMissingEpisodes(missing)
			} else {
				return domain.MediaRequest{}, domain.ErrRequestInLibrary
			}
		}
	}
	request, err := s.store.UpsertMediaRequest(ctx, input)
	if err != nil {
		return domain.MediaRequest{}, fmt.Errorf("保存求剧记录失败：%w", err)
	}
	return request, nil
}

// MyRequests returns the requests one account took part in (portal
// 我的求剧记录), newest requester activity first.
func (s *Service) MyRequests(ctx context.Context, accountID int64) ([]domain.MediaRequest, error) {
	return s.store.MyMediaRequests(ctx, accountID)
}

// missingEpisodeCount returns how many aired episodes of a series that already
// exists in Emby are missing relative to the TMDB catalog, aggregated per
// season. It is cheap (one Emby call plus one TMDB tv call, no per-season
// detail calls) and is used to annotate search results with "缺 N 集".
// Failures yield 0 so the search card falls back to the plain in-library mark.
func (s *Service) missingEpisodeCount(ctx context.Context, tmdbID int64) (int, error) {
	collected, present, err := s.emby.SeriesEpisodes(ctx, tmdbID)
	if err != nil || !present {
		return 0, nil
	}
	structure, err := s.tmdb.TVStructure(ctx, tmdbID)
	if err != nil {
		return 0, nil
	}
	total := 0
	for _, season := range structure.Seasons {
		if !aired(season.AirDate, s.now()) {
			continue
		}
		if inEmby := len(collected.Seasons[season.Number]); inEmby < season.EpisodeCount {
			total += season.EpisodeCount - inEmby
		}
	}
	return total, nil
}

// missingEpisodeDetails computes the exact missing episode numbers of a series
// already in Emby, season by season, enumerating TMDB season episode lists and
// subtracting what Emby collected. It feeds the 催更 record's episode list.
func (s *Service) missingEpisodeDetails(ctx context.Context, tmdbID int64) (map[int][]int, error) {
	collected, present, err := s.emby.SeriesEpisodes(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	if !present {
		// The series was reported present by the library check but cannot be
		// located now; treat it as complete to avoid a bogus nudge.
		return nil, nil
	}
	structure, err := s.tmdb.TVStructure(ctx, tmdbID)
	if err != nil {
		return nil, err
	}
	missing := make(map[int][]int)
	for _, season := range structure.Seasons {
		if !aired(season.AirDate, s.now()) {
			continue
		}
		if len(collected.Seasons[season.Number]) >= season.EpisodeCount {
			continue
		}
		expected, err := s.tmdb.SeasonEpisodes(ctx, tmdbID, season.Number)
		if err != nil {
			continue // skip a season that cannot be enumerated
		}
		gap := make([]int, 0, len(expected))
		for _, episode := range expected {
			if !collected.Seasons[season.Number][episode] {
				gap = append(gap, episode)
			}
		}
		if len(gap) > 0 {
			missing[season.Number] = gap
		}
	}
	return missing, nil
}

// aired reports whether a season likely has aired (its first air date is
// known and not in the future). Unknown dates count as aired so episodes are
// not silently dropped.
func aired(airDate string, now time.Time) bool {
	date, err := time.Parse("2006-01-02", strings.TrimSpace(airDate))
	if err != nil {
		return true
	}
	return !date.After(now)
}

// formatMissingEpisodes renders a season->episode map as a compact Chinese
// label, e.g. "S01E02、S01E04 · S02E01".
func formatMissingEpisodes(missing map[int][]int) string {
	seasons := make([]int, 0, len(missing))
	for season := range missing {
		seasons = append(seasons, season)
	}
	sort.Ints(seasons)
	parts := make([]string, 0, len(seasons))
	for _, season := range seasons {
		episodes := missing[season]
		sort.Ints(episodes)
		labels := make([]string, 0, len(episodes))
		for _, episode := range episodes {
			labels = append(labels, fmt.Sprintf("S%02dE%02d", season, episode))
		}
		parts = append(parts, strings.Join(labels, "、"))
	}
	return strings.Join(parts, " · ")
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
