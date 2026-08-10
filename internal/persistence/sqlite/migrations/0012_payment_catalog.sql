CREATE TABLE payment_plans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL CHECK (kind IN ('activation', 'renewal')),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    duration_days INTEGER NOT NULL CHECK (duration_days > 0),
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    price_fen INTEGER NOT NULL CHECK (price_fen > 0),
    note TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_payment_plans_kind_enabled ON payment_plans(kind, enabled, sort_order, id);

CREATE TABLE payment_orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    public_token TEXT NOT NULL UNIQUE,
    merchant_order_no TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('activation', 'renewal')),
    plan_id INTEGER NOT NULL REFERENCES payment_plans(id),
    plan_name TEXT NOT NULL,
    account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    account_username TEXT NOT NULL DEFAULT '',
    duration_days INTEGER NOT NULL CHECK (duration_days > 0),
    duration_minutes INTEGER NOT NULL CHECK (duration_minutes > 0),
    amount_fen INTEGER NOT NULL CHECK (amount_fen > 0),
    currency TEXT NOT NULL CHECK (currency = 'CNY'),
    payment_status TEXT NOT NULL CHECK (payment_status IN ('pending', 'paid', 'expired', 'canceled', 'failed')),
    fulfillment_status TEXT NOT NULL CHECK (fulfillment_status IN ('pending', 'completed', 'failed')),
    provider_status TEXT NOT NULL DEFAULT '',
    payment_url TEXT NOT NULL DEFAULT '',
    payment_memo TEXT NOT NULL DEFAULT '',
    provider_payment_key TEXT NOT NULL DEFAULT '',
    invite_id INTEGER REFERENCES invite_codes(id) ON DELETE SET NULL,
    failure_reason TEXT NOT NULL DEFAULT '',
    expires_at TEXT NOT NULL,
    paid_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_payment_orders_pending ON payment_orders(payment_status, fulfillment_status, updated_at);
CREATE INDEX idx_payment_orders_account ON payment_orders(account_id, created_at DESC);

CREATE TABLE payment_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    payment_order_id INTEGER NOT NULL REFERENCES payment_orders(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    amount_fen INTEGER NOT NULL,
    currency TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    received_at TEXT NOT NULL,
    processed_at TEXT NOT NULL
);

CREATE INDEX idx_payment_events_order ON payment_events(payment_order_id, id);
