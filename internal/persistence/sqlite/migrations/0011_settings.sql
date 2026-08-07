-- Application settings (key/value). The display time zone is stored here so
-- administrators can change it at runtime without restarting the server.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;
