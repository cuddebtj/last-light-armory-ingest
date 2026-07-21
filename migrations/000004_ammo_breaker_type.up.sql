-- 000004_ammo_breaker_type.up.sql
-- ammo_type (Primary/Special/Heavy) and breaker_type (intrinsic champion-
-- breaking capability, e.g. "Shield Piercing" = anti-Barrier) are Bungie
-- identity facts: equippingBlock.ammoType and breakerType/breakerTypeHash
-- on DestinyInventoryItemDefinition. Slot is not a valid proxy for ammo
-- type (Eriana's Vow is a Special-ammo Hand Cannon in the Energy slot).
-- Verified live 2026-07-21 against a fresh manifest pull.

BEGIN;

ALTER TABLE weapon ADD COLUMN ammo_type TEXT;
ALTER TABLE weapon ADD COLUMN breaker_type TEXT;

COMMIT;
