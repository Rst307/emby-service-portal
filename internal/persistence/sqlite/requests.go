package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

// UpsertMediaRequest records a media request, reactivating a previous request
// for the same account and TMDB title when one already exists. Reactivation
// lets a user re-request a title that was previously rejected.
func (s *Store) UpsertMediaRequest(ctx context.Context, input domain.CreateMediaRequestInput) (domain.MediaRequest, error) {
	now := input.Now.UTC()
	_, err := s.db.ExecContext(ctx, `INSERT INTO media_requests(account_id, account_username, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, kind, episodes, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)
ON CONFLICT(account_id, tmdb_id, media_type) DO UPDATE SET
  account_username = excluded.account_username,
  title = excluded.title,
  original_title = excluded.original_title,
  overview = excluded.overview,
  poster_path = excluded.poster_path,
  release_date = excluded.release_date,
  kind = excluded.kind,
  episodes = excluded.episodes,
  status = 'pending',
  updated_at = excluded.updated_at`,
		input.AccountID, input.AccountUsername, input.TmdbID, input.MediaType,
		input.Title, input.OriginalTitle, input.Overview, input.PosterPath, input.ReleaseDate,
		input.Kind, input.Episodes,
		timestamp(now), timestamp(now))
	if err != nil {
		return domain.MediaRequest{}, err
	}
	return s.FindMediaRequestByAccountTmdb(ctx, input.AccountID, input.TmdbID, input.MediaType)
}

// FindMediaRequestByAccountTmdb returns the current request row for one account
// and TMDB title.
func (s *Store) FindMediaRequestByAccountTmdb(ctx context.Context, accountID int64, tmdbID int64, mediaType string) (domain.MediaRequest, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, account_id, account_username, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, kind, episodes, status, created_at, updated_at
FROM media_requests WHERE account_id = ? AND tmdb_id = ? AND media_type = ?`, accountID, tmdbID, mediaType)
	return scanMediaRequest(row)
}

// ListMediaRequestsForAccount returns every request row of one account keyed by
// "mediaType:tmdbID" so search results can mark titles the user already asked
// for, together with each row's status.
func (s *Store) ListMediaRequestsForAccount(ctx context.Context, accountID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT media_type, tmdb_id, status FROM media_requests WHERE account_id = ?`, accountID)
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

// ListMediaRequests returns a bounded, filterable slice of media requests with
// pending/fulfilled totals for the current filter.
func (s *Store) ListMediaRequests(ctx context.Context, filter domain.MediaRequestFilter) (domain.MediaRequestPage, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	where, args := mediaRequestClause(filter.Status, filter.Query)
	base := ` FROM media_requests` + where

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+base, args...).Scan(&total); err != nil {
		return domain.MediaRequestPage{}, err
	}
	page := domain.MediaRequestPage{Requests: nil, Total: total, Page: filter.Page, PageSize: filter.PageSize}
	if filter.Status == "" {
		// Unfiltered view keeps actionable summary counts beside the list.
		pendingWhere, pendingArgs := mediaRequestClause(domain.MediaRequestPending, filter.Query)
		fulfilledWhere, fulfilledArgs := mediaRequestClause(domain.MediaRequestFulfilled, filter.Query)
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_requests`+pendingWhere, pendingArgs...).Scan(&page.Pending); err != nil {
			return domain.MediaRequestPage{}, err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_requests`+fulfilledWhere, fulfilledArgs...).Scan(&page.Fulfilled); err != nil {
			return domain.MediaRequestPage{}, err
		}
	}
	page.TotalPages = (total + filter.PageSize - 1) / filter.PageSize

	query := `SELECT id, account_id, account_username, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, kind, episodes, status, created_at, updated_at
FROM media_requests` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, query, listArgs...)
	if err != nil {
		return domain.MediaRequestPage{}, err
	}
	defer rows.Close()
	requests := make([]domain.MediaRequest, 0)
	for rows.Next() {
		request, err := scanMediaRequest(rows)
		if err != nil {
			return domain.MediaRequestPage{}, err
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return domain.MediaRequestPage{}, err
	}
	page.Requests = requests
	return page, nil
}

// mediaRequestClause builds the WHERE clause (with leading " WHERE") and its
// arguments for a status-filtered, keyword-filtered request list.
func mediaRequestClause(status, query string) (string, []any) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 2)
	if status = strings.TrimSpace(status); status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, status)
	}
	if query = strings.TrimSpace(query); query != "" {
		clauses = append(clauses, "(title LIKE ? OR original_title LIKE ? OR account_username LIKE ? OR CAST(tmdb_id AS TEXT) LIKE ?)")
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
	err := row.Scan(&request.ID, &request.AccountID, &request.AccountUsername, &request.TmdbID, &request.MediaType,
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
