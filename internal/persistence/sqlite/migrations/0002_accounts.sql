CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    emby_user_id TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled', 'expired', 'pending')),
    expires_at TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    disabled_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_accounts_expires_at ON accounts(expires_at);
CREATE INDEX IF NOT EXISTS idx_accounts_status ON accounts(status);
