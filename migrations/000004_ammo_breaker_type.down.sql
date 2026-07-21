-- 000004_ammo_breaker_type.down.sql

BEGIN;

ALTER TABLE weapon DROP COLUMN IF EXISTS breaker_type;
ALTER TABLE weapon DROP COLUMN IF EXISTS ammo_type;

COMMIT;
