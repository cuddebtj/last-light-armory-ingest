-- 000002_consumption_indexes.down.sql

BEGIN;

DROP INDEX IF EXISTS roll_weapon_score_idx;
DROP INDEX IF EXISTS perk_name_idx;
DROP INDEX IF EXISTS weapon_name_idx;
DROP INDEX IF EXISTS weapon_obtainable_idx;
DROP INDEX IF EXISTS weapon_tier_idx;
DROP INDEX IF EXISTS weapon_element_idx;
DROP INDEX IF EXISTS weapon_slot_idx;
DROP INDEX IF EXISTS weapon_weapon_type_idx;

COMMIT;
