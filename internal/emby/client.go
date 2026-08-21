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

// UserFinder is intentionally narrow: provisioning recovery only needs to
// establish whether the exact requested name was created before a lost reply.
type UserFinder interface {
	FindUserByUsername(ctx context.Context, username string) (User, error)
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

// libraryItem is the minimal Emby item shape needed to build a provider-ID set.
type libraryItem struct {
	Type        string            `json:"Type"`
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
