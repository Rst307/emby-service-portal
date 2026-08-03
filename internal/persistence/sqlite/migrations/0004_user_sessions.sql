CREATE TABLE user_sessions (
    id TEXT PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_user_sessions_token_hash ON user_sessions(token_hash);
