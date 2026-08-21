package domain

import (
	"errors"
	"time"
)

// Media request lifecycle states. New requests start as pending; an
// administrator marks them fulfilled once the media is added to the library or
// rejected when it cannot be added.
const (
	MediaRequestPending   = "pending"
	MediaRequestFulfilled = "fulfilled"
	MediaRequestRejected  = "rejected"
)

// Request kinds distinguish an add-to-library request (full) from a nudge that
// only asks to backfill missing episodes of a series that already exists
// (missing).
const (
	MediaRequestKindFull    = "full"
	MediaRequestKindMissing = "missing"
)

// MediaRequest records that a portal user asked for a movie or TV show to be
// added to the Emby library (Kind full) or for missing episodes of a series to
// be backfilled (Kind missing). Title and provider fields are snapshotted at
// request time so the record stays readable even if the upstream catalog
// changes. One business account can request a given TMDB title only once; a
// rejected request can be re-requested by reactivating the same row.
type MediaRequest struct {
	ID              int64
	AccountID       int64
	AccountUsername string
	TmdbID          int64
	MediaType       string // movie | tv
	Title           string
	OriginalTitle   string
	Overview        string
	PosterPath      string
	ReleaseDate     string
	Kind            string // full | missing
	// Episodes is the human-readable missing-episode summary for 催更
	// (Kind missing), e.g. "S01E02 S01E04 · 第 2 季缺 2 集".
	Episodes  string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateMediaRequestInput carries the fields a portal user submits. The server
// re-fetches catalog details from TMDB rather than trusting client values.
type CreateMediaRequestInput struct {
	AccountID       int64
	AccountUsername string
	TmdbID          int64
	MediaType       string
	Title           string
	OriginalTitle   string
	Overview        string
	PosterPath      string
	ReleaseDate     string
	Kind            string // full | missing
	Episodes        string
	Now             time.Time
}

// MediaRequestFilter controls the administrator request list. Page numbers are 1-based.
type MediaRequestFilter struct {
	Query    string
	Status   string
	Page     int
	PageSize int
}

// MediaRequestPage is a bounded slice of media requests plus totals for the current filter.
type MediaRequestPage struct {
	Requests   []MediaRequest
	Total      int
	Pending    int
	Fulfilled  int
	Page       int
	PageSize   int
	TotalPages int
}

var (
	// ErrRequestInLibrary rejects a request when the title already exists in
	// the Emby library (checked again on the server at submission time).
	ErrRequestInLibrary = errors.New("media already exists in the Emby library")
	// ErrRequestNotFound is returned by status/delete operations on a missing request.
	ErrRequestNotFound = errors.New("media request not found")
)
