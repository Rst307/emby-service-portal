// Package emby encapsulates the narrow Emby operations this application needs.
package emby

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrUserNotFound = errors.New("emby user not found")

type Client interface {
	CreateUser(ctx context.Context, username, password string) (User, error)
	DeleteUser(ctx context.Context, userID string) error
	SetUserDisabled(ctx context.Context, userID string, disabled bool) error
}

type Authenticator interface {
	AuthenticateUser(ctx context.Context, username, password string) (User, error)
}

type PolicyRestricter interface {
	RestrictUserMediaFeatures(ctx context.Context, userID string) error
}

type UserLister interface {
	ListUsers(ctx context.Context) ([]ManagedUser, error)
}

// ProviderLibrary reports which requested TMDB IDs are present in the Emby
// library under the given media types. The media request feature relies on it
// to mark search results that already exist.
type ProviderLibrary interface {
	AnyProviderIDExists(ctx context.Context, mediaTypes []string, tmdbIDs []int64) (map[string]bool, error)
}

// SeasonEpisodes is the per-series episode footprint Emby has collected, keyed
// by season number. The request service compares it against the TMDB catalog
// to compute missing episodes for the 催更 (nudge) workflow.
type SeasonEpisodes struct {
	// Seasons maps season number to the set of episode numbers collected.
	Seasons map[int]map[int]bool
}

// EpisodeLibrary reports the episodes Emby has for a TV series identified by
// its TMDB provider ID, together with whether the series exists at all.
type EpisodeLibrary interface {
	SeriesEpisodes(ctx context.Context, tmdbID int64) (SeasonEpisodes, bool, error)
}

// UserFinder is intentionally narrow: provisioning recovery only needs to
// establish whether the exact requested name was created before a lost reply.
type UserFinder interface {
	FindUserByUsername(ctx context.Context, username string) (User, error)
}

// RecentlyAddedItem is the minimal shape of one newly added library item. The
// 最近更新 feed on the portal and the request auto-fulfillment rely on it;
// TmdbID is 0 when Emby has no TMDB provider ID for the item.
type RecentlyAddedItem struct {
	ID   string
	Name string
	// Type uses the TMDB vocabulary (movie | tv) so it matches media_requests
	// rows directly.
	Type string
	// TmdbID is 0 when the item carries no TMDB provider ID.
	TmdbID int64
	// DateCreated is the library-created time (UTC). Zero when Emby did not
	// return a parseable timestamp; callers fall back to their own clock.
	DateCreated time.Time
}

// LibraryWatcher reports the newest items added to the Emby library. The
// recent-additions feed and automatic request fulfillment rely on it.
type LibraryWatcher interface {
	RecentlyAdded(ctx context.Context, limit int) ([]RecentlyAddedItem, error)
}

// PosterProvider streams item artwork (e.g. the primary poster) from Emby so
// the portal can proxy images server-side; browsers never see the API key.
type PosterProvider interface {
	ItemPoster(ctx context.Context, itemID string, maxWidth, maxHeight int) (io.ReadCloser, string, error)
}

type PasswordSetter interface {
	SetUserPassword(ctx context.Context, userID, password string) error
}
type ManagedUser struct {
	ID       string `json:"Id"`
	Username string `json:"Name"`
	Policy   struct {
		IsAdministrator bool `json:"IsAdministrator"`
		IsDisabled      bool `json:"IsDisabled"`
	} `json:"Policy"`
}

type User struct {
	ID       string `json:"Id"`
	Username string `json:"Name"`
}

type HTTPClient struct {
	baseURL    *url.URL
	apiKey     string
	httpClient *http.Client
}

func NewHTTPClient(baseURL, apiKey string) (*HTTPClient, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid Emby base URL")
	}
	return &HTTPClient{
		baseURL: u, apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			// Never forward the administrator token or a user's password to a
			// redirect target. Emby endpoints are expected to be stable URLs.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *HTTPClient) FindUserByUsername(ctx context.Context, username string) (User, error) {
	endpoint := c.endpoint("Users")
	query := endpoint.Query()
	query.Set("Name", username)
	endpoint.RawQuery = query.Encode()
	request, err := c.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return User{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return User{}, fmt.Errorf("find Emby user: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return User{}, ErrUserNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return User{}, fmt.Errorf("find Emby user returned HTTP %d", response.StatusCode)
	}
	var users []User
	if err := json.NewDecoder(response.Body).Decode(&users); err != nil {
		return User{}, fmt.Errorf("decode Emby user lookup: %w", err)
	}
	for _, user := range users {
		if user.ID != "" && strings.EqualFold(strings.TrimSpace(user.Username), strings.TrimSpace(username)) {
			return user, nil
		}
	}
	return User{}, ErrUserNotFound
}

func (c *HTTPClient) ListUsers(ctx context.Context) ([]ManagedUser, error) {
	request, err := c.request(ctx, http.MethodGet, c.endpoint("Users"), nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list Emby users: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("list Emby users returned HTTP %d", response.StatusCode)
	}
	var users []ManagedUser
	if err := json.NewDecoder(response.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("decode Emby users: %w", err)
	}
	return users, nil
}

// AnyProviderIDExists reports which of the supplied TMDB IDs exist in the Emby
// library under the given media types (e.g. Movie, Series). It issues one
// request using Emby's AnyProviderIdEquals filter, so a search result set can
// be marked against the live library without downloading the whole catalog.
// The returned map keys are "<mediaType>:<tmdbID>" using each item's own type.
func (c *HTTPClient) AnyProviderIDExists(ctx context.Context, mediaTypes []string, tmdbIDs []int64) (map[string]bool, error) {
	matched := make(map[string]bool)
	if len(tmdbIDs) == 0 {
		return matched, nil
	}
	// Emby expects the AnyProviderIdEquals form "prov.id", e.g. "tmdb.155"
	// (dot-separated). A colon form is not matched, silently producing empty
	// results — the root cause of titles showing as absent when they exist.
	tokens := make([]string, 0, len(tmdbIDs))
	tokenIDs := make(map[string]bool, len(tmdbIDs))
	for _, id := range tmdbIDs {
		tokens = append(tokens, fmt.Sprintf("tmdb.%d", id))
		for _, mediaType := range mediaTypes {
			tokenIDs[fmt.Sprintf("%s:%d", mediaType, id)] = true
		}
	}
	endpoint := c.endpoint("Items")
	query := endpoint.Query()
	query.Set("Recursive", "true")
	query.Set("IncludeItemTypes", strings.Join(embyItemTypes(mediaTypes), ","))
	query.Set("AnyProviderIdEquals", strings.Join(tokens, ","))
	query.Set("Fields", "ProviderIds")
	query.Set("Limit", "200")
	endpoint.RawQuery = query.Encode()
	request, err := c.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("check Emby library: %w", err)
	}
	items, err := decodeItemsWithProviderIDs(response)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		mediaType := normalizeMediaType(item.Type)
		key := fmt.Sprintf("%s:%s", mediaType, item.ProviderIDs["Tmdb"])
		if tokenIDs[key] {
			matched[key] = true
		}
	}
	return matched, nil
}

// embyItemTypes maps TMDB-vocabulary media types to the Emby BaseItemKind
// names the IncludeItemTypes filter matches on (Movie, Series).
func embyItemTypes(tmdbTypes []string) []string {
	types := make([]string, 0, len(tmdbTypes))
	for _, mediaType := range tmdbTypes {
		switch strings.ToLower(strings.TrimSpace(mediaType)) {
		case "tv":
			types = append(types, "Series")
		case "movie":
			types = append(types, "Movie")
		default:
			types = append(types, strings.TrimSpace(mediaType))
		}
	}
	if len(types) == 0 {
		types = []string{"Movie", "Series"}
	}
	return dedupeStrings(types)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

// SeriesEpisodes reports which episodes Emby has collected for a TV series
// identified by its TMDB provider ID, and whether the series is present at all.
func (c *HTTPClient) SeriesEpisodes(ctx context.Context, tmdbID int64) (SeasonEpisodes, bool, error) {
	series, found, err := c.findSeriesByTmdb(ctx, tmdbID)
	if err != nil {
		return SeasonEpisodes{}, false, err
	}
	if !found {
		return SeasonEpisodes{}, false, nil
	}
	endpoint := c.endpoint("Shows", series.ID, "Episodes")
	request, err := c.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return SeasonEpisodes{}, false, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return SeasonEpisodes{}, false, fmt.Errorf("query Emby series episodes: %w", err)
	}
	episodes := make(map[int]map[int]bool)
	if err := decodeSeasonEpisodes(response, episodes); err != nil {
		return SeasonEpisodes{}, false, err
	}
	return SeasonEpisodes{Seasons: episodes}, true, nil
}

// findSeriesByTmdb locates one Emby Series item by its TMDB provider ID.
func (c *HTTPClient) findSeriesByTmdb(ctx context.Context, tmdbID int64) (struct{ ID string }, bool, error) {
	endpoint := c.endpoint("Items")
	query := endpoint.Query()
	query.Set("Recursive", "true")
	query.Set("IncludeItemTypes", "Series")
	query.Set("AnyProviderIdEquals", fmt.Sprintf("tmdb.%d", tmdbID))
	query.Set("Fields", "ProviderIds")
	query.Set("Limit", "1")
	endpoint.RawQuery = query.Encode()
	request, err := c.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return struct{ ID string }{}, false, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return struct{ ID string }{}, false, fmt.Errorf("find Emby series: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return struct{ ID string }{}, false, fmt.Errorf("find Emby series returned HTTP %d", response.StatusCode)
	}
	var wrapped struct {
		Items []struct {
			ID        string            `json:"Id"`
			Type      string            `json:"Type"`
			Providers map[string]string `json:"ProviderIds"`
		} `json:"Items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&wrapped); err != nil {
		return struct{ ID string }{}, false, fmt.Errorf("decode Emby series lookup: %w", err)
	}
	for _, item := range wrapped.Items {
		if renderID, ok := item.Providers["Tmdb"]; ok && renderID == fmt.Sprintf("%d", tmdbID) {
			return struct{ ID string }{ID: item.ID}, true, nil
		}
	}
	return struct{ ID string }{}, false, nil
}

// decodeSeasonEpisodes fills a season -> set(episode numbers) map from an Emby
// /Shows/{id}/Episodes response (a bare array or a wrapped items list).
func decodeSeasonEpisodes(response *http.Response, seasons map[int]map[int]bool) error {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("emby series episodes returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read Emby series episodes response: %w", err)
	}
	var wrapped struct {
		Items []struct {
			SeasonNumber  int `json:"ParentIndexNumber"`
			EpisodeNumber int `json:"IndexNumber"`
		} `json:"Items"`
	}
	var items []struct {
		SeasonNumber  int `json:"ParentIndexNumber"`
		EpisodeNumber int `json:"IndexNumber"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Items != nil {
		items = wrapped.Items
	} else if err := json.Unmarshal(body, &items); err != nil {
		return fmt.Errorf("decode Emby series episodes response: %w", err)
	}
	for _, episode := range items {
		if episode.SeasonNumber < 1 || episode.EpisodeNumber < 1 {
			continue
		}
		if seasons[episode.SeasonNumber] == nil {
			seasons[episode.SeasonNumber] = make(map[int]bool)
		}
		seasons[episode.SeasonNumber][episode.EpisodeNumber] = true
	}
	return nil
}

// RecentlyAdded returns the newest library items of the main media types
// (Movie, Series), newest first, using Emby's DateCreated field so the portal
// 最近更新 feed reflects actual library additions. It relies on Providers
// being returned; items without a TMDB provider ID still appear, with
// TmdbID = 0.
func (c *HTTPClient) RecentlyAdded(ctx context.Context, limit int) ([]RecentlyAddedItem, error) {
	if limit < 1 {
		limit = 30
	}
	endpoint := c.endpoint("Items")
	query := endpoint.Query()
	query.Set("Recursive", "true")
	query.Set("IncludeItemTypes", "Movie,Series")
	query.Set("SortBy", "DateCreated")
	query.Set("SortOrder", "Descending")
	query.Set("Fields", "ProviderIds,DateCreated")
	query.Set("Limit", strconv.Itoa(limit))
	endpoint.RawQuery = query.Encode()
	request, err := c.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query Emby recent additions: %w", err)
	}
	items, err := decodeItemsWithProviderIDs(response)
	if err != nil {
		return nil, err
	}
	out := make([]RecentlyAddedItem, 0, len(items))
	for _, item := range items {
		created, _ := parseEmbyTime(item.DateCreated)
		tmdbID, _ := strconv.ParseInt(item.ProviderIDs["Tmdb"], 10, 64)
		out = append(out, RecentlyAddedItem{
			ID: item.ID, Name: item.Name, Type: normalizeMediaType(item.Type),
			TmdbID: tmdbID, DateCreated: created,
		})
	}
	return out, nil
}

// ItemPoster streams the primary image for one item (itemID is used verbatim
// as the Emby route segment; the caller decides whether the ID is trusted).
// maxWidth/maxHeight are optional constraints; zero values are omitted. The
// returned reader must be closed by the caller.
func (c *HTTPClient) ItemPoster(ctx context.Context, itemID string, maxWidth, maxHeight int) (io.ReadCloser, string, error) {
	endpoint := c.endpoint("Items/" + url.PathEscape(itemID) + "/Images/Primary")
	query := endpoint.Query()
	if maxWidth > 0 {
		query.Set("maxWidth", strconv.Itoa(maxWidth))
	}
	if maxHeight > 0 {
		query.Set("maxHeight", strconv.Itoa(maxHeight))
	}
	query.Set("quality", "90")
	endpoint.RawQuery = query.Encode()
	request, err := c.request(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("fetch Emby poster: %w", err)
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, "", fmt.Errorf("emby poster %q: %s", itemID, response.Status)
	}
	return response.Body, contentType, nil
}

// parseEmbyTime parses the timestamp shapes Emby emits for BaseItem dates: the
// legacy .NET form "/Date(1423987200000)/" (milliseconds since the Unix
// epoch) and ISO 8601 with fractional seconds (e.g.
// "2021-08-16T18:04:16.0000000Z"). Unrecognized values yield a zero time so
// callers can apply their own fallback.
func parseEmbyTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "/Date(") {
		raw := strings.TrimPrefix(value, "/Date(")
		raw = strings.TrimSuffix(raw, ")/")
		ms, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("unrecognized Emby timestamp %q", value)
		}
		return time.UnixMilli(ms).UTC(), nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.9999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized Emby timestamp %q", value)
}

// normalizeMediaType maps an Emby item type to the TMDB media-type vocabulary
// used by the request records (Movie -> movie, Series -> tv).
func normalizeMediaType(embyType string) string {
	switch strings.ToLower(strings.TrimSpace(embyType)) {
	case "series":
		return "tv"
	default:
		return strings.ToLower(strings.TrimSpace(embyType))
	}
}

// libraryItem is the minimal Emby item shape needed for provider-ID checks and
// the recent-additions feed.
type libraryItem struct {
	ID          string            `json:"Id"`
	Name        string            `json:"Name"`
	Type        string            `json:"Type"`
	DateCreated string            `json:"DateCreated"`
	ProviderIDs map[string]string `json:"ProviderIds"`
}

// decodeItemsWithProviderIDs parses an Emby Items response. Emby returns a bare
// array unless paging is requested; some versions wrap it as {"Items":[...]},
// so both shapes are accepted.
func decodeItemsWithProviderIDs(response *http.Response) ([]libraryItem, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("query Emby library returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read Emby library response: %w", err)
	}
	// Emby returns a bare item array unless paging is requested; some versions
	// wrap it as {"Items": [...]}. Try the wrapper first, then the array form.
	var wrapped struct {
		Items []libraryItem `json:"Items"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Items != nil {
		return wrapped.Items, nil
	}
	var items []libraryItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("decode Emby library response: %w", err)
	}
	return items, nil
}

func (c *HTTPClient) AuthenticateUser(ctx context.Context, username, password string) (User, error) {
	body, err := json.Marshal(map[string]string{"Username": username, "Pw": password})
	if err != nil {
		return User{}, fmt.Errorf("encode Emby credentials: %w", err)
	}
	request, err := c.request(ctx, http.MethodPost, c.endpoint("Users", "AuthenticateByName"), bytes.NewReader(body))
	if err != nil {
		return User{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Emby-Authorization", `Emby Client="Emby Service Portal", Device="Web", DeviceId="emby-service-portal", Version="1.0.0"`)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return User{}, fmt.Errorf("send Emby authentication request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return User{}, fmt.Errorf("emby authentication returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		User User `json:"User"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return User{}, fmt.Errorf("decode Emby authentication response: %w", err)
	}
	if payload.User.ID == "" {
		return User{}, fmt.Errorf("emby authentication response has no user ID")
	}
	return payload.User, nil
}

func (c *HTTPClient) CreateUser(ctx context.Context, username, password string) (User, error) {
	body, err := json.Marshal(map[string]string{"Name": username})
	if err != nil {
		return User{}, fmt.Errorf("encode Emby user: %w", err)
	}
	request, err := c.request(ctx, http.MethodPost, c.endpoint("Users/New"), bytes.NewReader(body))
	if err != nil {
		return User{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return User{}, fmt.Errorf("send Emby user creation request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return User{}, fmt.Errorf("emby user creation returned HTTP %d", response.StatusCode)
	}
	var user User
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		return User{}, fmt.Errorf("decode Emby user creation response: %w", err)
	}
	if user.ID == "" {
		return User{}, fmt.Errorf("emby user creation response has no user ID")
	}
	if err := c.SetUserPassword(ctx, user.ID, password); err != nil {
		_ = c.DeleteUser(ctx, user.ID)
		return User{}, err
	}
	return user, nil
}

func (c *HTTPClient) SetUserPassword(ctx context.Context, userID, password string) error {
	body, err := json.Marshal(map[string]string{"CurrentPw": "", "NewPw": password})
	if err != nil {
		return fmt.Errorf("encode Emby password: %w", err)
	}
	request, err := c.request(ctx, http.MethodPost, c.endpoint("Users", userID, "Password"), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("set Emby password: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("set Emby password returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *HTTPClient) DeleteUser(ctx context.Context, userID string) error {
	request, err := c.request(ctx, http.MethodDelete, c.endpoint("Users", userID), nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send Emby user deletion request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrUserNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Some Emby versions return 400/500 when DELETE targets a user that is
		// already absent. Confirm with a read before treating it as a failure.
		missing, err := c.userMissing(ctx, userID)
		if err == nil && missing {
			return ErrUserNotFound
		}
		return fmt.Errorf("emby user deletion returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *HTTPClient) userMissing(ctx context.Context, userID string) (bool, error) {
	request, err := c.request(ctx, http.MethodGet, c.endpoint("Users", userID), nil)
	if err != nil {
		return false, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusNotFound, nil
}

func (c *HTTPClient) RestrictUserMediaFeatures(ctx context.Context, userID string) error {
	return c.updateUserPolicy(ctx, userID, func(policy map[string]any) {
		for _, field := range []string{"EnableAudioPlaybackTranscoding", "EnableVideoPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableContentDownloading", "EnableSyncTranscoding", "EnableSubtitleDownloading", "EnableSubtitleManagement", "EnableMediaConversion", "AllowCameraUpload"} {
			policy[field] = false
		}
	})
}

func (c *HTTPClient) SetUserDisabled(ctx context.Context, userID string, disabled bool) error {
	return c.updateUserPolicy(ctx, userID, func(policy map[string]any) { policy["IsDisabled"] = disabled })
}

func (c *HTTPClient) updateUserPolicy(ctx context.Context, userID string, update func(map[string]any)) error {
	policyURL := c.endpoint("Users", userID, "Policy")
	get, err := c.request(ctx, http.MethodGet, c.endpoint("Users", userID), nil)
	if err != nil {
		return err
	}
	response, err := c.httpClient.Do(get)
	if err != nil {
		return fmt.Errorf("get Emby user policy: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("get Emby user policy returned HTTP %d", response.StatusCode)
	}
	var user struct {
		Policy map[string]any `json:"Policy"`
	}
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		return fmt.Errorf("decode Emby user policy: %w", err)
	}
	if user.Policy == nil {
		return fmt.Errorf("emby user response has no policy")
	}
	update(user.Policy)
	policy := user.Policy
	body, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode Emby policy: %w", err)
	}
	post, err := c.request(ctx, http.MethodPost, policyURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	post.Header.Set("Content-Type", "application/json")
	response, err = c.httpClient.Do(post)
	if err != nil {
		return fmt.Errorf("set Emby user policy: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("set Emby user policy returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (c *HTTPClient) endpoint(parts ...string) *url.URL {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.Join(parts, "/")
	return &endpoint
}
func (c *HTTPClient) request(ctx context.Context, method string, endpoint *url.URL, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build Emby request: %w", err)
	}
	request.Header.Set("X-Emby-Token", c.apiKey)
	return request, nil
}
