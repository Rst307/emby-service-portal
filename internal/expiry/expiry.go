// Package expiry reconciles expired business accounts and pending Emby access policy updates.
package expiry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
	"github.com/Rst307/emby-service-portal/internal/emby"
	"github.com/Rst307/emby-service-portal/internal/persistence/sqlite"
)

type Runner struct {
	store *sqlite.Store
	emby  emby.Client
	now   func() time.Time
}

func New(store *sqlite.Store, client emby.Client) *Runner {
	return &Runner{store: store, emby: client, now: time.Now}
}

// RunOnce records newly expired accounts first, then reconciles a bounded batch
// of desired Emby access states. Individual failures remain queued for a later
// retry and are returned so the caller can log and alert on them.
func (r *Runner) RunOnce(ctx context.Context) error {
	now := r.now().UTC()
	var failures []error
	due, err := r.store.ListDueActiveAccounts(ctx, now)
	if err != nil {
		return err
	}
	failures = append(failures, r.expireDue(ctx, due, now)...)

	jobs, err := r.store.ListAccessSyncJobs(ctx, 100)
	if err != nil {
		return errors.Join(append(failures, fmt.Errorf("list Emby access sync jobs: %w", err))...)
	}
	for _, job := range jobs {
		if err := r.emby.SetUserDisabled(ctx, job.Account.EmbyUserID, job.DesiredDisabled); err != nil {
			failures = append(failures, fmt.Errorf("synchronize %q access: %w", job.Account.Username, err))
			if recordErr := r.store.FailAccessSync(ctx, job.Account.ID, job.Revision, err.Error(), now); recordErr != nil {
				failures = append(failures, fmt.Errorf("record %q sync failure: %w", job.Account.Username, recordErr))
			}
			continue
		}
		if err := r.store.CompleteAccessSync(ctx, job.Account.ID, job.Revision); err != nil {
			failures = append(failures, fmt.Errorf("complete %q access sync: %w", job.Account.Username, err))
		}
	}
	return errors.Join(failures...)
}

func (r *Runner) expireDue(ctx context.Context, due []domain.Account, now time.Time) []error {
	var failures []error
	for _, account := range due {
		if _, err := r.store.SetAccountStatus(ctx, account, "expired", &now, now); err != nil {
			if errors.Is(err, domain.ErrAccountVersionConflict) {
				// A renewal or admin edit won the race after this scan. It has
				// recorded a newer desired state, so never queue a stale disable.
				continue
			}
			failures = append(failures, fmt.Errorf("mark %q expired: %w", account.Username, err))
		}
	}
	return failures
}
