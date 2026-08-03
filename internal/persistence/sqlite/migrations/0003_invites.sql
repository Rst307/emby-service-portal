CREATE TABLE IF NOT EXISTS invite_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code_hash TEXT NOT NULL UNIQUE,
    code_prefix TEXT NOT NULL,
    duration_days INTEGER NOT NULL CHECK (duration_days > 0),
    max_uses INTEGER NOT NULL CHECK (max_uses >= 0),
    used_count INTEGER NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    starts_at TEXT,
    expires_at TEXT,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    CHECK (expires_at IS NULL OR starts_at IS NULL OR expires_at > starts_at)
);

CREATE TABLE IF NOT EXISTS invite_redemptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    invite_code_id INTEGER NOT NULL REFERENCES invite_codes(id),
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    kind TEXT NOT NULL CHECK (kind IN ('register', 'renew')),
    duration_days INTEGER NOT NULL,
    redeemed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_invite_redemptions_account_id ON invite_redemptions(account_id);
