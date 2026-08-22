-- 求剧改为以影视条目为聚合单位:同一剧集多人求剧只保留一条 media_requests 记录,
-- 求剧用户列表移入 media_request_users(可多行),请求状态(pending/fulfilled/rejected)
-- 归属条目本身,管理端一次标记对全体求剧用户生效。
CREATE TABLE media_request_users (
    media_request_id INTEGER NOT NULL REFERENCES media_requests(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    account_username TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (media_request_id, account_id)
);
CREATE INDEX idx_media_request_users_account ON media_request_users(account_id, created_at DESC);

-- 数据迁移:每个 (tmdb_id, media_type) 保留最早(id 最小)的记录,
-- 其余记录的求剧人并入 users 表。
CREATE TABLE media_requests_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tmdb_id INTEGER NOT NULL CHECK (tmdb_id > 0),
    media_type TEXT NOT NULL CHECK (media_type IN ('movie', 'tv')),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    original_title TEXT NOT NULL DEFAULT '',
    overview TEXT NOT NULL DEFAULT '',
    poster_path TEXT NOT NULL DEFAULT '',
    release_date TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'fulfilled', 'rejected')),
    kind TEXT NOT NULL DEFAULT 'full',
    episodes TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
INSERT INTO media_requests_new (id, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, status, kind, episodes, created_at, updated_at)
SELECT id, tmdb_id, media_type, title, original_title, overview, poster_path, release_date, status, kind, episodes, created_at, updated_at
FROM media_requests
WHERE id = (SELECT MIN(id) FROM media_requests AS kept WHERE kept.tmdb_id = media_requests.tmdb_id AND kept.media_type = media_requests.media_type);

INSERT INTO media_request_users (media_request_id, account_id, account_username, created_at)
SELECT new_rows.id, old_rows.account_id, old_rows.account_username, old_rows.created_at
FROM media_requests AS old_rows
JOIN media_requests_new AS new_rows
  ON new_rows.tmdb_id = old_rows.tmdb_id AND new_rows.media_type = old_rows.media_type;

DROP TABLE media_requests;
ALTER TABLE media_requests_new RENAME TO media_requests;
CREATE UNIQUE INDEX idx_media_requests_tmdb ON media_requests(tmdb_id, media_type);
CREATE INDEX idx_media_requests_status ON media_requests(status, created_at DESC, id DESC);