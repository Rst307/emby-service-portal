package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

// UpsertMediaRequest records a media request aggregated per TMDB title: the
// first requester creates the row, further requesters of the same title join
// the existing row's requester list. A submission reactivates the request
// (status back to pending) so a rejected title can be re-requested. Callers
// with no business account (AccountID < 1, e.g. workflow submissions) record
// the title without attaching a requester.
func (s *Store) UpsertMediaRequest(ctx context.Context, input domain.CreateMediaRequestInput) (domain.MediaRequest, error) {
	now := input.Now.UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO media_requests(tmdb_id, media_type, title, original_title, overview, poster_path, release_date, kind, episodes, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)
ON CONFLICT(tmdb_id, media_type) DO UPDATE SET
  title = excluded.title,
  original_title = excluded.original_title,
  overview = excluded.overview,
  poster_path = excluded.poster_path,
  release_date = excluded.release_date,
  kind = excluded.kind,
  episodes = excluded.episodes,
  status = 'pending',
  updated_at = excluded.updated_at`,
		input.TmdbID, input.MediaType,
		input.Title, input.OriginalTitle, input.Overview, input.PosterPath, input.ReleaseDate,
		input.Kind, input.Episodes,
		timestamp(now), timestamp(now))
	if err != nil {
		return domain.MediaRequest{}, err
	}
	var requestID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM media_requests WHERE tmdb_id = ? AND media_type = ?`, input.TmdbID, input.MediaType).Scan(&requestID); err != nil {
		return domain.MediaRequest{}, err
	}
	if input.AccountID > 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO media_request_users(media_request_id, account_id, account_username, created_at) VALUES (?, ?, ?, ?)`,
			requestID, input.AccountID, input.AccountUsername, timestamp(now)); err != nil {
			return domain.MediaRequest{}, err
		}
	}
	return s.FindMediaRequestByTmdb(ctx, input.TmdbID, input.MediaType)
}

// FindMediaRequestByTmdb returns the aggregated request row (with its
// requester list) for one TMDB title.
func (s *Store) FindMediaRequestByTmdb(ctx context.Context, tmdbID int64, mediaType string) (domain.MediaRequest, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, kind, episodes, status, created_at, updated_at
FROM media_requests WHERE tmdb_id = ? AND media_type = ?`, tmdbID, mediaType)
	request, err := scanMediaRequest(row)
	if err != nil {
		return domain.MediaRequest{}, err
	}
	requesters, err := s.loadRequesters(ctx, []int64{request.ID})
	if err != nil {
		return domain.MediaRequest{}, err
	}
	request.Requesters = requesters[request.ID]
	return request, nil
}

// ListMediaRequestsForAccount returns every request status of one account keyed
// by "mediaType:tmdbID" so search results can mark titles the user already
// asked for.
func (s *Store) ListMediaRequestsForAccount(ctx context.Context, accountID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.media_type, m.tmdb_id, m.status
FROM media_request_users u JOIN media_requests m ON m.id = u.media_request_id
WHERE u.account_id = ?`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var mediaType string
		var tmdbID int64
		var status string
		if err := rows.Scan(&mediaType, &tmdbID, &status); err != nil {
			return nil, err
		}
		result[fmt.Sprintf("%s:%d", mediaType, tmdbID)] = status
	}
	return result, rows.Err()
}

// MyMediaRequests returns the aggregated requests one account took part in,
// newest requester activity first, for the portal 我的求剧记录 section.
func (s *Store) MyMediaRequests(ctx context.Context, accountID int64) ([]domain.MediaRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.tmdb_id, m.media_type, m.title, m.original_title, m.overview, m.poster_path, m.release_date, m.kind, m.episodes, m.status, m.created_at, m.updated_at
FROM media_request_users u JOIN media_requests m ON m.id = u.media_request_id
WHERE u.account_id = ? ORDER BY u.created_at DESC, m.id DESC LIMIT 50`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]domain.MediaRequest, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		request, err := scanMediaRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
		ids = append(ids, request.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return requests, nil
	}
	requesters, err := s.loadRequesters(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range requests {
		requests[i].Requesters = requesters[requests[i].ID]
	}
	return requests, nil
}

// ListMediaRequests returns a bounded, filterable slice of media requests with
// pending/fulfilled totals for the current filter.
func (s *Store) ListMediaRequests(ctx context.Context, filter domain.MediaRequestFilter) (domain.MediaRequestPage, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	where, args := mediaRequestClause(filter.Status, filter.Query, filter.TmdbID)
	base := ` FROM media_requests` + where

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+base, args...).Scan(&total); err != nil {
		return domain.MediaRequestPage{}, err
	}
	page := domain.MediaRequestPage{Requests: nil, Total: total, Page: filter.Page, PageSize: filter.PageSize}
	if filter.Status == "" {
		// Unfiltered view keeps actionable summary counts beside the list.
		pendingWhere, pendingArgs := mediaRequestClause(domain.MediaRequestPending, filter.Query, filter.TmdbID)
		fulfilledWhere, fulfilledArgs := mediaRequestClause(domain.MediaRequestFulfilled, filter.Query, filter.TmdbID)
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_requests`+pendingWhere, pendingArgs...).Scan(&page.Pending); err != nil {
			return domain.MediaRequestPage{}, err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_requests`+fulfilledWhere, fulfilledArgs...).Scan(&page.Fulfilled); err != nil {
			return domain.MediaRequestPage{}, err
		}
	}
	page.TotalPages = (total + filter.PageSize - 1) / filter.PageSize

	query := `SELECT id, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, kind, episodes, status, created_at, updated_at
FROM media_requests` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return domain.MediaRequestPage{}, err
	}
	defer rows.Close()
	requests := make([]domain.MediaRequest, 0)
	ids := make([]int64, 0)
	for rows.Next() {
		request, err := scanMediaRequest(rows)
		if err != nil {
			return domain.MediaRequestPage{}, err
		}
		requests = append(requests, request)
		ids = append(ids, request.ID)
	}
	if err := rows.Err(); err != nil {
		return domain.MediaRequestPage{}, err
	}
	if len(requests) > 0 {
		requesters, err := s.loadRequesters(ctx, ids)
		if err != nil {
			return domain.MediaRequestPage{}, err
		}
		for i := range requests {
			requests[i].Requesters = requesters[requests[i].ID]
		}
	}
	page.Requests = requests
	return page, nil
}

// loadRequesters loads the requester list of every given request in request
// order, keyed by media request id.
func (s *Store) loadRequesters(ctx context.Context, requestIDs []int64) (map[int64][]domain.MediaRequester, error) {
	result := make(map[int64][]domain.MediaRequester, len(requestIDs))
	if len(requestIDs) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(requestIDs)), ",")
	args := make([]any, 0, len(requestIDs))
	for _, id := range requestIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT media_request_id, account_id, account_username, created_at
FROM media_request_users WHERE media_request_id IN (`+placeholders+`) ORDER BY created_at ASC, account_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var requestID int64
		var requester domain.MediaRequester
		var created string
		if err := rows.Scan(&requestID, &requester.AccountID, &requester.AccountUsername, &created); err != nil {
			return nil, err
		}
		if requester.CreatedAt, err = parseTimestamp(created); err != nil {
			return nil, err
		}
		result[requestID] = append(result[requestID], requester)
	}
	return result, rows.Err()
}

// mediaRequestClause builds the WHERE clause (with leading " WHERE") and its
// arguments for a status-filtered, keyword-filtered request list. The keyword
// matches the title, the original title, the requester usernames, or the TMDB
// id.
func mediaRequestClause(status, query string, tmdbID int64) (string, []any) {
	clauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if status = strings.TrimSpace(status); status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if tmdbID > 0 {
		clauses = append(clauses, "tmdb_id = ?")
		args = append(args, tmdbID)
	}
	if query = strings.TrimSpace(query); query != "" {
		clauses = append(clauses, `(title LIKE ? OR original_title LIKE ? OR CAST(tmdb_id AS TEXT) LIKE ?
  OR EXISTS (SELECT 1 FROM media_request_users u WHERE u.media_request_id = media_requests.id AND u.account_username LIKE ?))`)
		like := "%" + query + "%"
		args = append(args, like, like, like, like)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// SetMediaRequestStatus transitions one request row to a new lifecycle state.
func (s *Store) SetMediaRequestStatus(ctx context.Context, id int64, status string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE media_requests SET status = ?, updated_at = ? WHERE id = ?`, status, timestamp(now.UTC()), id)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return domain.ErrRequestNotFound
	}
	return nil
}

func (s *Store) DeleteMediaRequest(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM media_requests WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return domain.ErrRequestNotFound
	}
	return nil
}

type mediaRequestScanner interface{ Scan(...any) error }

func scanMediaRequest(row mediaRequestScanner) (domain.MediaRequest, error) {
	var request domain.MediaRequest
	var created, updated string
	err := row.Scan(&request.ID, &request.TmdbID, &request.MediaType,
		&request.Title, &request.OriginalTitle, &request.Overview, &request.PosterPath, &request.ReleaseDate,
		&request.Kind, &request.Episodes, &request.Status, &created, &updated)
	if err != nil {
		return domain.MediaRequest{}, err
	}
	if request.CreatedAt, err = parseTimestamp(created); err != nil {
		return domain.MediaRequest{}, err
	}
	if request.UpdatedAt, err = parseTimestamp(updated); err != nil {
		return domain.MediaRequest{}, err
	}
	return request, nil
}
