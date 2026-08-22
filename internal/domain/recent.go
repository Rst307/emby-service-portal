package domain

import "time"

// RecentlyAdded is one library item Emby reported as newly added, recorded for
// the portal 最近更新 feed. Title, TMDB id and media type are snapshotted at
// discovery time; a later catalog rename does not rewrite history.
type RecentlyAdded struct {
	EmbyItemID string
	TmdbID     int64
	MediaType  string // movie | tv
	Title      string
	// DateCreated is the library-created time Emby reported (UTC).
	DateCreated time.Time
	// FirstSeenAt is when this portal first observed the item (UTC).
	FirstSeenAt time.Time
	// RequestID references the media_requests row that was auto-fulfilled when
	// this item matched a pending request (0 when no request matched).
	RequestID int64
}

// RecentlyAddedInput carries what a library watch scan discovered about one
// item. The store upserts by EmbyItemID so repeated scans are idempotent.
type RecentlyAddedInput struct {
	EmbyItemID  string
	TmdbID      int64
	MediaType   string
	Title       string
	DateCreated time.Time
	Now         time.Time
}
