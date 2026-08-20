CREATE TABLE media_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    account_username TEXT NOT NULL,
    tmdb_id INTEGER NOT NULL CHECK (tmdb_id > 0),
    media_type TEXT NOT NULL CHECK (media_type IN ('movie', 'tv')),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    original_title TEXT NOT NULL DEFAULT '',
    overview TEXT NOT NULL DEFAULT '',
    poster_path TEXT NOT NULL DEFAULT '',
    release_date TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'fulfilled', 'rejected')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_media_requests_account_tmdb ON media_requests(account_id, tmdb_id, media_type);
CREATE INDEX idx_media_requests_status ON media_requests(status, created_at DESC, id DESC);