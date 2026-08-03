ALTER TABLE invite_codes ADD COLUMN duration_minutes INTEGER NOT NULL DEFAULT 0;
UPDATE invite_codes SET duration_minutes = duration_days * 1440 WHERE duration_minutes = 0;
ALTER TABLE invite_redemptions ADD COLUMN duration_minutes INTEGER NOT NULL DEFAULT 0;
UPDATE invite_redemptions SET duration_minutes = duration_days * 1440 WHERE duration_minutes = 0;
