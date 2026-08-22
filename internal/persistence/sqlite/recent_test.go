package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Rst307/emby-service-portal/internal/domain"
)

func TestUpsertRecentlyAddedFulfillsMatchingPendingRequest(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	account := testAccount(t, ctx, store, "alice", now)

	request, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: 157336, MediaType: "movie", Title: "星际穿越", OriginalTitle: "Interstellar",
		Overview: "近未来地球荒芜", PosterPath: "/x.jpg", ReleaseDate: "2014-11-05",
		Kind: domain.MediaRequestKindFull, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != domain.MediaRequestPending {
		t.Fatalf("request status = %q, want pending", request.Status)
	}

	fulfilledID, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
		EmbyItemID: "item-m1", TmdbID: 157336, MediaType: "movie", Title: "星际穿越",
		DateCreated: now.Add(2 * time.Hour), Now: now.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fulfilledID != request.ID {
		t.Fatalf("fulfilled request = %d, want %d", fulfilledID, request.ID)
	}
	after, err := store.FindMediaRequestByTmdb(ctx, 157336, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != domain.MediaRequestFulfilled {
		t.Fatalf("request status after match = %q, want fulfilled", after.Status)
	}

	items, err := store.ListRecentlyAdded(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("recent items = %d, want 1", len(items))
	}
	item := items[0]
	if item.EmbyItemID != "item-m1" || item.TmdbID != 157336 || item.MediaType != "movie" || item.Title != "星际穿越" {
		t.Fatalf("recent item = %+v", item)
	}
	if item.RequestID != request.ID {
		t.Fatalf("item request link = %d, want %d", item.RequestID, request.ID)
	}
	if !item.DateCreated.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("item DateCreated = %s, want %s", item.DateCreated, now.Add(2*time.Hour))
	}
	if !item.FirstSeenAt.Equal(now.Add(3 * time.Hour)) {
		t.Fatalf("item FirstSeenAt = %s, want %s", item.FirstSeenAt, now.Add(3*time.Hour))
	}
}

func TestUpsertRecentlyAddedSkipsMissingAndRejectedRequests(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 2, 1, 0, 0, 0, 0, time.UTC)
	account := testAccount(t, ctx, store, "bob", now)

	// 催更 (missing) 只针对缺失剧集:新增一个 Series 条目并不等于补齐缺集,
	// 因此绝不自动标记已入库。
	nudge, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: 46952, MediaType: "tv", Title: "苍穹浩瀚", OriginalTitle: "The Expanse",
		Kind: domain.MediaRequestKindMissing, Episodes: "S01E04", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 已驳回的求剧保持驳回状态,只有用户主动重新求剧才会回到待处理。
	rejected, err := store.UpsertMediaRequest(ctx, domain.CreateMediaRequestInput{
		AccountID: account.ID, AccountUsername: account.Username,
		TmdbID: 155, MediaType: "movie", Title: "蝙蝠侠：黑暗骑士", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMediaRequestStatus(ctx, rejected.ID, domain.MediaRequestRejected, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	requestID, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
		EmbyItemID: "item-s1", TmdbID: 46952, MediaType: "tv", Title: "苍穹浩瀚",
		DateCreated: now.Add(time.Hour), Now: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestID != 0 {
		t.Fatalf("missing-kind request must not be fulfilled, got request %d", requestID)
	}
	requestID, err = store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
		EmbyItemID: "item-m2", TmdbID: 155, MediaType: "movie", Title: "蝙蝠侠：黑暗骑士",
		DateCreated: now.Add(90 * time.Minute), Now: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestID != 0 {
		t.Fatalf("rejected request must not be fulfilled, got request %d", requestID)
	}

	checkReqs, err := store.ListMediaRequestsForAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checkReqs["tv:46952"] != domain.MediaRequestPending {
		t.Fatalf("nudge status = %q, want pending", checkReqs["tv:46952"])
	}
	if checkReqs["movie:155"] != domain.MediaRequestRejected {
		t.Fatalf("rejected status = %q, want rejected", checkReqs["movie:155"])
	}
	// 两条记录都落库,request_id 均为 0。
	items, err := store.ListRecentlyAdded(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].RequestID != 0 || items[1].RequestID != 0 {
		t.Fatalf("recent items = %+v", items)
	}
	if nudge.ID == rejected.ID {
		t.Fatal("test setup error: requests must be distinct")
	}
}

func TestUpsertRecentlyAddedDeduplicatesByEmbyItemID(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if _, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
			EmbyItemID: "item-dupe", TmdbID: 157336, MediaType: "movie", Title: "星际穿越",
			DateCreated: now, Now: now.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListRecentlyAdded(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("duplicate scan rows = %d, want 1", len(items))
	}
	if !items[0].FirstSeenAt.Equal(now) {
		t.Fatalf("FirstSeenAt = %s, want first-scan time %s", items[0].FirstSeenAt, now)
	}
}

func TestPruneRecentlyAddedKeepsNewestOnly(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir()+"/manager.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2030, 4, 1, 0, 0, 0, 0, time.UTC)

	for i := 1; i <= 4; i++ {
		if _, err := store.UpsertRecentlyAdded(ctx, domain.RecentlyAddedInput{
			EmbyItemID: string(rune('a' + i)), TmdbID: int64(i), MediaType: "movie",
			Title: "T", DateCreated: now.Add(time.Duration(i) * time.Hour), Now: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PruneRecentlyAdded(ctx, 2, now.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListRecentlyAdded(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("after prune = %d rows, want 2", len(items))
	}
	if items[0].TmdbID != 4 || items[1].TmdbID != 3 {
		t.Fatalf("prune kept wrong rows: %+v", items)
	}
}
