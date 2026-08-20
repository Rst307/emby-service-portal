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
	"time"
)

const (
	MediaTypeMovie = "movie"
	MediaTypeTV    = "tv"
)

var ErrNotConfigured = errors.New("TMDB API key is not configured")

// PosterBaseURL serves TMDB poster paths as absolute image URLs with a fixed
// width (w342) that is readable in both card and table layouts.
const PosterBaseURL = "https://image.tmdb.org/t/p/w342"

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
// the item has no artwork on TMDB).
func PosterURL(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return PosterBaseURL + path
}

// Client queries the TMDB v3 API with a read access token or API key.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// tmdbBaseURL is the public TMDB API root. Tests override it with a stub.
const tmdbBaseURL = "https://api.themoviedb.org/3"

// NewClient returns a TMDB client. An empty apiKey leaves the client
// unconfigured: Configured() reports false and searches fail with
// ErrNotConfigured so deployments without TMDB can keep every other feature.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:     strings.TrimSpace(apiKey),
		baseURL:    tmdbBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Configured() bool { return c.apiKey != "" }

// SetBaseURL overrides the public TMDB endpoint. It exists for tests and for
// deployments mirroring the TMDB API; production uses the public endpoint.
func (c *Client) SetBaseURL(baseURL string) { c.baseURL = strings.TrimRight(baseURL, "/") }

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
