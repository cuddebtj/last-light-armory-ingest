-- 000002_consumption_indexes.up.sql
-- Indexes for the read paths that consume this data: the static-export
-- queries in this repo, the scoring job in last-light-armory, and any
-- ad-hoc analysis. Foreign keys and uniqueness already exist from 000001;
-- these are purely for query performance.
--
-- Deliberately no trigram/fulltext indexes: the website is statically
-- generated and does its searching client-side over exported JSON, so
-- fuzzy-search never reaches Postgres.

BEGIN;

-- Website/export facet filters: browse by type, slot, element, tier.
CREATE INDEX weapon_weapon_type_idx ON weapon (weapon_type);
CREATE INDEX weapon_slot_idx ON weapon (slot);
CREATE INDEX weapon_element_idx ON weapon (element);
CREATE INDEX weapon_tier_idx ON weapon (tier);

-- Partial index: "currently obtainable" is the default consumer filter and
-- true for only part of the table.
CREATE INDEX weapon_obtainable_idx ON weapon (obtainable) WHERE obtainable;

-- Name lookups (exact/prefix) for exports, scoring joins, and debugging.
CREATE INDEX weapon_name_idx ON weapon (name);
CREATE INDEX perk_name_idx ON perk (name);

-- "Top rolls for a weapon" — ready for when last-light-armory writes
-- overall_score; NULLS LAST keeps unscored rolls out of the way.
CREATE INDEX roll_weapon_score_idx ON roll (weapon_id, overall_score DESC NULLS LAST);

COMMIT;
