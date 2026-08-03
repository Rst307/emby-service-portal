-- Existing codes were intentionally stored only as hashes and cannot be recovered.
-- New codes are retained for authenticated administrator copy actions.
ALTER TABLE invite_codes ADD COLUMN code TEXT;
