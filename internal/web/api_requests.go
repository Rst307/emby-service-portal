package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/requests"
	"github.com/Rst307/emby-service-portal/internal/tmdb"
)

// requestJSON is the workflow-facing representation of a media request
// (GET/POST /api/v1/requests). A request aggregates per TMDB title: multiple
// users asking for the same title share one record and its status.
type requestJSON struct {
	ID             int64           `json:"id"`
	Requesters     []requesterJSON `json:"requesters"`
	RequesterCount int             `json:"requester_count"`
	TmdbID         int64           `json:"tmdb_id"`
	MediaType      string          `json:"media_type"`
	Title          string          `json:"title"`
	OriginalTitle  string          `json:"original_title"`
	Overview       string          `json:"overview"`
	PosterPath     string          `json:"poster_path"`
	ReleaseDate    string          `json:"release_date"`
	Kind           string          `json:"kind"`
	Episodes       string          `json:"episodes,omitempty"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type requesterJSON struct {
	AccountID       int64     `json:"account_id"`
	AccountUsername string    `json:"account_username"`
	CreatedAt       time.Time `json:"created_at"`
}

func requestJSONFrom(request domain.MediaRequest) requestJSON {
	requesters := make([]requesterJSON, 0, len(request.Requesters))
	for _, requester := range request.Requesters {
		requesters = append(requesters, requesterJSON{
			AccountID: requester.AccountID, AccountUsername: requester.AccountUsername, CreatedAt: requester.CreatedAt,
		})
	}
	return requestJSON{
		ID: request.ID, Requesters: requesters, RequesterCount: len(requesters),
		TmdbID: request.TmdbID, MediaType: request.MediaType, Title: request.Title,
		OriginalTitle: request.OriginalTitle, Overview: request.Overview, PosterPath: request.PosterPath,
		ReleaseDate: request.ReleaseDate, Kind: request.Kind, Episodes: request.Episodes,
		Status: request.Status, CreatedAt: request.CreatedAt, UpdatedAt: request.UpdatedAt,
	}
}

func requestJSONList(requests []domain.MediaRequest) []requestJSON {
	output := make([]requestJSON, 0, len(requests))
	for _, request := range requests {
		output = append(output, requestJSONFrom(request))
	}
	return output
}

// apiRequests lists media requests for workflow integration. Query params:
// status (pending|fulfilled|rejected, empty = all), tmdb_id (exact match),
// q (title/original-title/username/tmdb-id substring), page, page_size
// (default 20, capped at 100 by the store).
func (s *Server) apiRequests(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	filter := domain.MediaRequestFilter{
		Query:    strings.TrimSpace(r.URL.Query().Get("q")),
		Status:   normalizeRequestStatus(r.URL.Query().Get("status")),
		PageSize: 20,
	}
	if page, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && page > 0 {
		filter.Page = page
	}
	if pageSize, err := strconv.Atoi(r.URL.Query().Get("page_size")); err == nil && pageSize > 0 {
		filter.PageSize = pageSize
	}
	if tmdbID, err := strconv.ParseInt(r.URL.Query().Get("tmdb_id"), 10, 64); err == nil && tmdbID > 0 {
		filter.TmdbID = tmdbID
	}
	result, err := s.requests.List(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load media requests"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requests":    requestJSONList(result.Requests),
		"total":       result.Total,
		"pending":     result.Pending,
		"fulfilled":   result.Fulfilled,
		"page":        result.Page,
		"page_size":   result.PageSize,
		"total_pages": result.TotalPages,
	})
}

// apiCreateRequest submits a media request from a workflow. The server
// re-fetches catalog details from TMDB (never trusts client values), checks
// the Emby library, and aggregates per (tmdb_id, media_type): re-submitting an
// existing title joins the requester list and resets it to pending. account_id
// is optional attribution: when provided it must reference an existing
// business account (it is attached as the requester), otherwise the request
// is recorded without a requester.
func (s *Server) apiCreateRequest(w http.ResponseWriter, r *http.Request) {
	if !s.requireAPIKey(w, r) {
		return
	}
	var input struct {
		AccountID int64  `json:"account_id"`
		MediaType string `json:"media_type"`
		TmdbID    int64  `json:"tmdb_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if (input.MediaType != tmdb.MediaTypeMovie && input.MediaType != tmdb.MediaTypeTV) || input.TmdbID < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "media_type must be movie or tv and tmdb_id must be positive"})
		return
	}
	account := domain.Account{}
	if input.AccountID > 0 {
		var err error
		account, err = s.accounts.Get(r.Context(), input.AccountID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id does not reference an existing account"})
			return
		}
	}
	request, err := s.requests.Create(r.Context(), account, input.MediaType, input.TmdbID)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, tmdb.ErrNotConfigured):
			status = http.StatusServiceUnavailable
		case errors.Is(err, domain.ErrRequestInLibrary):
			status = http.StatusConflict
		case errors.Is(err, requests.ErrTitleNotFound):
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, requestJSONFrom(request))
}

// apiRequestSetStatus marks a request fulfilled or rejected so workflows can
// close the loop (e.g. after a downloader job completes).
func (s *Server) apiRequestSetStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAPIKey(w, r) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request ID"})
			return
		}
		if err := s.requests.SetStatus(r.Context(), id, status); err != nil {
			if errors.Is(err, domain.ErrRequestNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "media request not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update media request"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status})
	}
}
