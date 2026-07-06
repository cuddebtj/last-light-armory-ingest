-- 000001_init.down.sql
-- Reverses 000001_init.up.sql. Order matters: children before parents.

BEGIN;

DROP TABLE IF EXISTS weapon_ranking;
DROP TABLE IF EXISTS roll_perk;
DROP TABLE IF EXISTS roll;
DROP TABLE IF EXISTS weapon_perk;
DROP TABLE IF EXISTS perk;
DROP TABLE IF EXISTS weapon;
DROP TABLE IF EXISTS manifest_sync_state;

COMMIT;
