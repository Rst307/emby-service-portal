// Package tmdb wraps the narrow The Movie Database (TMDB) operations the media
// request feature needs: multi search and single-item details lookup with
// Chinese-language results.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	MediaTypeMovie = "movie"
	MediaTypeTV    = "tv"
)

var ErrNotConfigured = errors.New("TMDB API key is not configured")

// DefaultPosterBaseURL serves TMDB poster paths as absolute image URLs with a
// fixed width (w342) readable in both card and table layouts. Deployments where
// the public TMDB image CDN is unreachable override it with SetPosterBaseURL
// (ESP_TMDB_IMAGE_BASE_URL).
const DefaultPosterBaseURL = "https://image.tmdb.org/t/p/w342"

// posterBase holds the runtime poster base URL. It is process-global so that
// tmdb.PosterURL (used by template rendering) and the page Content-Security-
// Policy share one value; it defaults to the public CDN and is swapped once at
// startup from ESP_TMDB_IMAGE_BASE_URL.
var posterBase atomic.Value // stores string

func init() {
	posterBase.Store(DefaultPosterBaseURL)
}

func currentPosterBase() string {
	if value, ok := posterBase.Load().(string); ok && value != "" {
		return value
	}
	return DefaultPosterBaseURL
}

// SetPosterBaseURL overrides the poster CDN used by PosterURL and the page CSP.
// It accepts a TMDB-compatible image root (default https://image.tmdb.org/t/p/
// w342) from a mirror such as a TMDB image reverse proxy.
func SetPosterBaseURL(base string) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base != "" {
		posterBase.Store(base)
	}
}

// PosterBaseHost returns the scheme://host of the configured poster CDN, for
// inclusion in the page img-src Content-Security-Policy directive.
func PosterBaseHost() string {
	u, err := url.Parse(currentPosterBase())
	if err != nil || u.Scheme == "" || u.Host == "" {
		return DefaultPosterBaseURL
	}
	return u.Scheme + "://" + u.Host
}

// Result is one movie or TV show returned by a TMDB search or details lookup.
// Field names reuse the shared surface across the two media types.
type Result struct {
	ID            int64
	MediaType     string // movie | tv
	Title         string
	OriginalTitle string
	Overview      string
	PosterPath    string
	ReleaseDate   string
}

// PosterURL builds an absolute poster URL from a stored poster path ("" when
// the item has no artwork on TMDB). It renders through the configured poster
// base so a deployment can substitute a reachable image mirror.
func PosterURL(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return currentPosterBase() + path
}

// Client queries the TMDB v3 API with a read access token or API key.
type Client struct {
	apiKey          string
	baseURL         string
	fallbackBaseURL string
	timeout         time.Duration
	proxyURL        *url.URL
	httpClient      *http.Client
}

// tmdbBaseURL is the public TMDB API root. Tests override it with a stub.
const tmdbBaseURL = "https://api.themoviedb.org/3"

// defaultTimeout bounds each TMDB API request. Deployments behind slow or
// congested links raise it via SetTimeout (ESP_TMDB_TIMEOUT) so a slow mirror
// does not surface as "no results".
const defaultTimeout = 10 * time.Second

// NewClient returns a TMDB client. An empty apiKey leaves the client
// unconfigured: Configured() reports false and searches fail with
// ErrNotConfigured so deployments without TMDB can keep every other feature.
func NewClient(apiKey string) *Client {
	c := &Client{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: tmdbBaseURL,
		timeout: defaultTimeout,
	}
	c.rebuildHTTPClient()
	return c
}

// rebuildHTTPClient wires the HTTP transport. By default the transport honors
// the process HTTP(S)_PROXY environment variables; SetProxy forces a specific
// proxy instead.
func (c *Client) rebuildHTTPClient() {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	if c.proxyURL != nil {
		transport.Proxy = http.ProxyURL(c.proxyURL)
	}
	c.httpClient = &http.Client{Transport: transport, Timeout: c.timeout}
}

// SetTimeout overrides the per-request timeout (default 10s).
func (c *Client) SetTimeout(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	c.timeout = timeout
	c.rebuildHTTPClient()
}

// SetProxy routes TMDB API requests through the given HTTP(S) or SOCKS5
// proxy. This is how deployments where api.themoviedb.org is slow or
// unreachable (e.g. mainland China) reach the API through a reachable proxy or
// a local reverse proxy.
func (c *Client) SetProxy(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid TMDB proxy URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("unsupported TMDB proxy scheme %q (want http, https or socks5)", u.Scheme)
	}
	c.proxyURL = u
	c.rebuildHTTPClient()
	return nil
}

func (c *Client) Configured() bool { return c.apiKey != "" }

// SetBaseURL overrides the public TMDB endpoint. It exists for tests and for
// deployments mirroring the TMDB API (e.g. ESP_TMDB_BASE_URL pointing at a
// reachable mirror in mainland China). The official endpoint is kept as a
// fallback: when a mirrored request fails it is retried once against it
// through the same transport (proxy), so a flaky mirror degrades gracefully.
func (c *Client) SetBaseURL(baseURL string) {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" || baseURL == tmdbBaseURL {
		c.baseURL = tmdbBaseURL
		c.fallbackBaseURL = ""
		return
	}
	c.fallbackBaseURL = tmdbBaseURL
	c.baseURL = baseURL
}

// SearchMulti returns movies and TV shows matching the query, localized to
// Simplified Chinese where TMDB has translations.
func (c *Client) SearchMulti(ctx context.Context, query string, limit int) ([]Result, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if limit < 1 || limit > 20 {
		limit = 20
	}
	endpoint := c.baseURL + "/search/multi?api_key=" + url.QueryEscape(c.apiKey) +
		"&language=zh-CN&query=" + url.QueryEscape(query) + "&include_adult=false&page=1"
	payload, err := c.getJSON(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var body struct {
		Results []struct {
			ID            int64  `json:"id"`
			MediaType     string `json:"media_type"`
			Title         string `json:"title"`
			Name          string `json:"name"`
			OriginalTitle string `json:"original_title"`
			OriginalName  string `json:"original_name"`
			Overview      string `json:"overview"`
			PosterPath    string `json:"poster_path"`
			ReleaseDate   string `json:"release_date"`
			FirstAirDate  string `json:"first_air_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("decode TMDB search: %w", err)
	}
	results := make([]Result, 0, len(body.Results))
	for _, item := range body.Results {
		if item.MediaType != MediaTypeMovie && item.MediaType != MediaTypeTV {
			continue
		}
		result := Result{
			ID: item.ID, MediaType: item.MediaType,
			Title: firstNonEmpty(item.Title, item.Name),
		}
		// Movies and TV shows use different namespaces on TMDB.
		if item.MediaType == MediaTypeMovie {
			result.OriginalTitle = item.OriginalTitle
			result.ReleaseDate = item.ReleaseDate
		} else {
			result.OriginalTitle = item.OriginalName
			result.ReleaseDate = item.FirstAirDate
		}
		result.Overview = item.Overview
		result.PosterPath = item.PosterPath
		results = append(results, result)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// Details returns the authoritative catalog record for one TMDB item. The
// server uses it to re-validate a request submission instead of trusting data
// echoed by the browser.
func (c *Client) Details(ctx context.Context, mediaType string, id int64) (Result, bool, error) {
	if !c.Configured() {
		return Result{}, false, ErrNotConfigured
	}
	if mediaType != MediaTypeMovie && mediaType != MediaTypeTV {
		return Result{}, false, fmt.Errorf("unsupported TMDB media type %q", mediaType)
	}
	endpoint := fmt.Sprintf("%s/%s/%d?api_key=%s&language=zh-CN", c.baseURL, url.PathEscape(mediaType), id, url.QueryEscape(c.apiKey))
	status, payload, err := c.get(ctx, endpoint)
	if err != nil {
		return Result{}, false, err
	}
	if status == http.StatusNotFound {
		return Result{}, false, nil
	}
	if status < 200 || status >= 300 {
		return Result{}, false, fmt.Errorf("TMDB details returned HTTP %d", status)
	}
	var body struct {
		ID            int64  `json:"id"`
		Title         string `json:"title"`
		Name          string `json:"name"`
		OriginalTitle string `json:"original_title"`
		OriginalName  string `json:"original_name"`
		Overview      string `json:"overview"`
		PosterPath    string `json:"poster_path"`
		ReleaseDate   string `json:"release_date"`
		FirstAirDate  string `json:"first_air_date"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return Result{}, false, fmt.Errorf("decode TMDB details: %w", err)
	}
	if body.ID == 0 {
		return Result{}, false, nil
	}
	result := Result{ID: body.ID, MediaType: mediaType, Title: firstNonEmpty(body.Title, body.Name), Overview: body.Overview, PosterPath: body.PosterPath}
	if mediaType == MediaTypeMovie {
		result.OriginalTitle = body.OriginalTitle
		result.ReleaseDate = body.ReleaseDate
	} else {
		result.OriginalTitle = body.OriginalName
		result.ReleaseDate = body.FirstAirDate
	}
	return result, true, nil
}

// Season is one season's shape from the TMDB tv-details payload.
type Season struct {
	Number       int
	EpisodeCount int
	AirDate      string // empty for specials before any air date is known
}

// TVStructure is the season layout of a TV series, used to compare the TMDB
// catalog against what Emby actually collected so missing episodes can be
// reported for the 催更 (nudge) workflow.
type TVStructure struct {
	NumberOfSeasons int
	Seasons         []Season
}

// TVStructure returns the season layout for one TV series from /tv/{id}. When
// the title is not a series this still parses an empty structure.
func (c *Client) TVStructure(ctx context.Context, id int64) (TVStructure, error) {
	if !c.Configured() {
		return TVStructure{}, ErrNotConfigured
	}
	endpoint := fmt.Sprintf("%s/tv/%d?api_key=%s&language=zh-CN", c.baseURL, id, url.QueryEscape(c.apiKey))
	payload, err := c.getJSON(ctx, endpoint)
	if err != nil {
		return TVStructure{}, err
	}
	var body struct {
		NumberOfSeasons int `json:"number_of_seasons"`
		Seasons         []struct {
			SeasonNumber int    `json:"season_number"`
			EpisodeCount int    `json:"episode_count"`
			AirDate      string `json:"air_date"`
		} `json:"seasons"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return TVStructure{}, fmt.Errorf("decode TMDB tv structure: %w", err)
	}
	structure := TVStructure{NumberOfSeasons: body.NumberOfSeasons}
	structure.Seasons = make([]Season, 0, len(body.Seasons))
	for _, season := range body.Seasons {
		if season.SeasonNumber < 1 {
			continue // skip specials (season 0)
		}
		structure.Seasons = append(structure.Seasons, Season{
			Number: season.SeasonNumber, EpisodeCount: season.EpisodeCount,
			AirDate: season.AirDate,
		})
	}
	return structure, nil
}

// SeasonEpisodes returns the episode numbers of one season from
// /tv/{id}/season/{season}. Specials are skipped.
func (c *Client) SeasonEpisodes(ctx context.Context, id int64, season int) ([]int, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	endpoint := fmt.Sprintf("%s/tv/%d/season/%d?api_key=%s&language=zh-CN", c.baseURL, id, season, url.QueryEscape(c.apiKey))
	payload, err := c.getJSON(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var body struct {
		Episodes []struct {
			EpisodeNumber int `json:"episode_number"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, fmt.Errorf("decode TMDB season: %w", err)
	}
	numbers := make([]int, 0, len(body.Episodes))
	for _, episode := range body.Episodes {
		if episode.EpisodeNumber >= 1 {
			numbers = append(numbers, episode.EpisodeNumber)
		}
	}
	return numbers, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string) ([]byte, error) {
	status, body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("TMDB returned HTTP %d", status)
	}
	return body, nil
}

func (c *Client) get(ctx context.Context, endpoint string) (int, []byte, error) {
	status, body, err := c.do(ctx, endpoint)
	if err == nil && status >= 200 && status < 300 {
		return status, body, nil
	}
	// Mirror-first, official-fallback: when a mirror override is active and the
	// mirrored call failed, retry against the official host so deployments can
	// keep both a fast mirror and a proxied path to TMDB.
	fallback := c.fallbackEndpoint(endpoint)
	if fallback == "" {
		return status, body, err
	}
	if fbStatus, fbBody, fbErr := c.do(ctx, fallback); fbErr == nil && fbStatus >= 200 && fbStatus < 500 {
		return fbStatus, fbBody, nil
	}
	return status, body, err
}

// fallbackEndpoint rewrites an endpoint URL onto the official TMDB host when a
// mirror override is active, or returns "" when there is nothing to fall back
// to (no override configured).
func (c *Client) fallbackEndpoint(endpoint string) string {
	if c.baseURL == tmdbBaseURL || c.fallbackBaseURL == "" {
		return ""
	}
	current, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	official, err := url.Parse(c.fallbackBaseURL)
	if err != nil {
		return ""
	}
	current.Scheme = official.Scheme
	current.Host = official.Host
	return current.String()
}

func (c *Client) do(ctx context.Context, endpoint string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build TMDB request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("query TMDB: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read TMDB response: %w", err)
	}
	return response.StatusCode, body, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
