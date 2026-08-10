package sqlite

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) ListAccessSyncJobs(ctx context.Context, limit int) ([]AccessSyncJob, error) {
	if limit < 1 {
		return []AccessSyncJob{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.version, a.emby_user_id, a.username, a.status, a.expires_at, a.note, a.created_at, a.updated_at, a.disabled_at, j.desired_disabled, j.revision, j.attempts, j.last_error FROM emby_access_sync_jobs j JOIN accounts a ON a.id = j.account_id ORDER BY j.updated_at, j.account_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]AccessSyncJob, 0)
	for rows.Next() {
		job, err := scanAccessSyncJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) CompleteAccessSync(ctx context.Context, accountID, revision int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM emby_access_sync_jobs WHERE account_id = ? AND revision = ?`, accountID, revision)
	return err
}

func (s *Store) FailAccessSync(ctx context.Context, accountID, revision int64, message string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE emby_access_sync_jobs SET attempts = attempts + 1, last_error = ?, updated_at = ? WHERE account_id = ? AND revision = ?`, message, timestamp(now), accountID, revision)
	return err
}

type accessSyncJobScanner interface{ Scan(...any) error }

func scanAccessSyncJob(row accessSyncJobScanner) (AccessSyncJob, error) {
	var job AccessSyncJob
	var expires, created, updated string
	var disabled sql.NullString
	var desired int
	if err := row.Scan(&job.Account.ID, &job.Account.Version, &job.Account.EmbyUserID, &job.Account.Username, &job.Account.Status, &expires, &job.Account.Note, &created, &updated, &disabled, &desired, &job.Revision, &job.Attempts, &job.LastError); err != nil {
		return AccessSyncJob{}, err
	}
	var err error
	if job.Account.ExpiresAt, err = parseTimestamp(expires); err != nil {
		return AccessSyncJob{}, err
	}
	if job.Account.CreatedAt, err = parseTimestamp(created); err != nil {
		return AccessSyncJob{}, err
	}
	if job.Account.UpdatedAt, err = parseTimestamp(updated); err != nil {
		return AccessSyncJob{}, err
	}
	if disabled.Valid {
		parsed, err := parseTimestamp(disabled.String)
		if err != nil {
			return AccessSyncJob{}, err
		}
		job.Account.DisabledAt = &parsed
	}
	job.DesiredDisabled = desired != 0
	return job, nil
}
