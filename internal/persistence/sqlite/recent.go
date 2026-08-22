package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

// UpsertRecentlyAdded records one newly added Emby library item and, in the
// same transaction, auto-fulfills a pending media request that matches its
// TMDB id and media type. Only full (求剧) requests are auto-fulfilled: a 催更
// (missing) request asks for episodes, which a Series item addition does not
// deliver. The returned requestID is the fulfilled media_requests row id, or 0
// when nothing matched. Repeated scans of the same Emby item are idempotent;
// an already-fulfilled match is never overwritten or re-applied.
func (s *Store) UpsertRecentlyAdded(ctx context.Context, input domain.RecentlyAddedInput) (int64, error) {
	now := input.Now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var requestID int64
	if input.TmdbID > 0 {
		err := tx.QueryRowContext(ctx, `SELECT id FROM media_requests WHERE tmdb_id = ? AND media_type = ? AND status = 'pending' AND kind = 'full' LIMIT 1`,
			input.TmdbID, input.MediaType).Scan(&requestID)
		if err != nil && err != sql.ErrNoRows {
			return 0, err
		}
		if requestID > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE media_requests SET status = 'fulfilled', updated_at = ? WHERE id = ?`, timestamp(now), requestID); err != nil {
				return 0, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO recently_added(emby_item_id, tmdb_id, media_type, title, date_created, first_seen_at, request_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(emby_item_id) DO UPDATE SET
  tmdb_id = excluded.tmdb_id,
  media_type = excluded.media_type,
  title = excluded.title,
  date_created = excluded.date_created,
  request_id = CASE WHEN recently_added.request_id = 0 THEN excluded.request_id ELSE recently_added.request_id END,
  updated_at = excluded.updated_at`,
		input.EmbyItemID, input.TmdbID, input.MediaType, input.Title,
		timestamp(input.DateCreated), timestamp(now), requestID, timestamp(now)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return requestID, nil
}

// ListRecentlyAdded returns the newest recorded library additions, newest
// first, for the portal 最近更新 feed.
func (s *Store) ListRecentlyAdded(ctx context.Context, limit int) ([]domain.RecentlyAdded, error) {
	if limit < 1 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT emby_item_id, tmdb_id, media_type, title, date_created, first_seen_at, request_id
FROM recently_added ORDER BY date_created DESC, emby_item_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.RecentlyAdded, 0)
	for rows.Next() {
		var item domain.RecentlyAdded
		var created, firstSeen string
		if err := rows.Scan(&item.EmbyItemID, &item.TmdbID, &item.MediaType, &item.Title, &created, &firstSeen, &item.RequestID); err != nil {
			return nil, err
		}
		if item.DateCreated, err = parseTimestamp(created); err != nil {
			return nil, err
		}
		if item.FirstSeenAt, err = parseTimestamp(firstSeen); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PruneRecentlyAdded removes everything beyond the newest `keep` items so the
// feed does not grow without bound.
func (s *Store) PruneRecentlyAdded(ctx context.Context, keep int, now time.Time) error {
	if keep < 1 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM recently_added WHERE emby_item_id NOT IN
(SELECT emby_item_id FROM recently_added ORDER BY date_created DESC, emby_item_id DESC LIMIT ?)`, keep)
	return err
}
