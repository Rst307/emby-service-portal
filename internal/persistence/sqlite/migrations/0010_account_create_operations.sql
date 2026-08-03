CREATE TABLE account_create_operations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK (kind IN ('account_create', 'register')),
    idempotency_key TEXT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    username TEXT NOT NULL,
    password_ciphertext TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    invite_code_hash TEXT,
    invite_code_id INTEGER REFERENCES invite_codes(id),
    invite_duration_days INTEGER NOT NULL DEFAULT 0,
    invite_duration_minutes INTEGER NOT NULL DEFAULT 0,
    emby_user_id TEXT,
    account_id INTEGER REFERENCES accounts(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'creating', 'remote_created', 'completed')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT,
    UNIQUE(kind, idempotency_key),
    CHECK ((kind = 'account_create' AND invite_code_hash IS NULL AND invite_code_id IS NULL AND invite_duration_days = 0 AND invite_duration_minutes = 0) OR
           (kind = 'register' AND invite_code_hash IS NOT NULL AND invite_code_id IS NOT NULL AND invite_duration_days > 0 AND invite_duration_minutes > 0)),
    CHECK ((status = 'completed' AND account_id IS NOT NULL AND emby_user_id IS NOT NULL AND completed_at IS NOT NULL) OR
           (status <> 'completed' AND account_id IS NULL)),
    CHECK ((status IN ('remote_created', 'completed') AND emby_user_id IS NOT NULL) OR
           (status IN ('pending', 'creating') AND emby_user_id IS NULL))
);

CREATE INDEX idx_account_create_operations_pending ON account_create_operations(status, updated_at);
