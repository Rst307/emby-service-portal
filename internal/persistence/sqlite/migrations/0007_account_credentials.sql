CREATE TABLE account_credentials (
    account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    password_ciphertext TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
