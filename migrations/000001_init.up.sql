-- 000001_init.up.sql
-- Initial schema for the last_light_armory database.
--
-- Ownership split (confirmed 2026-07-06):
--   * This repo (last-light-armory-ingest) owns the schema and writes only
--     Bungie-sourced structural facts.
--   * The sibling repo (last-light-armory) owns every *_score column and the
--     weapon_ranking table; this repo creates them but never writes a score.
--
-- All ingest writes are idempotent upserts keyed on Bungie hash values.

BEGIN;

-- Single-row bookkeeping table: which manifest version we last saw, and when.
CREATE TABLE manifest_sync_state (
    id                    SMALLINT PRIMARY KEY DEFAULT 1,
    last_manifest_version TEXT NOT NULL,
    last_checked_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_changed_at       TIMESTAMPTZ,
    CHECK (id = 1)
);

-- One row per weapon inventory item, keyed on Bungie's item hash.
-- weapon_type doubles as the archetype (decision 2026-07-06: archetype is
-- the weapon type — "Auto Rifle", "Sniper Rifle", etc. — so the separate
-- archetype column from the original draft schema was dropped).
CREATE TABLE weapon (
    id          BIGSERIAL PRIMARY KEY,
    hash        BIGINT UNIQUE NOT NULL,          -- Bungie DestinyInventoryItemDefinition hash
    name        TEXT NOT NULL,
    weapon_type TEXT NOT NULL,                   -- e.g. "Auto Rifle" (also the archetype)
    frame       TEXT,                            -- intrinsic perk name, e.g. "Precision Frame"
    rpm         INTEGER,                         -- fire-rate stat (RPM; draw/charge time ms for bows/fusions)
    slot        TEXT NOT NULL,                   -- Kinetic / Energy / Power
    element     TEXT,                            -- damage type name
    tier        TEXT,                            -- e.g. "Legendary", "Exotic"
    source      TEXT,                            -- collectible sourceString, when present
    craftable   BOOLEAN NOT NULL DEFAULT FALSE,
    enhanceable BOOLEAN NOT NULL DEFAULT FALSE,
    obtainable  BOOLEAN NOT NULL DEFAULT FALSE,  -- heuristic v1: active collectible OR craftable
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per perk plug item, keyed on Bungie's item hash.
-- pve_score / pvp_score are curated by last-light-armory; ingest writes NULL.
CREATE TABLE perk (
    id         BIGSERIAL PRIMARY KEY,
    hash       BIGINT UNIQUE NOT NULL,           -- Bungie DestinyInventoryItemDefinition hash
    name       TEXT NOT NULL,
    enhanced   BOOLEAN NOT NULL DEFAULT FALSE,   -- enhanced-trait variant of a base perk
    pve_score  SMALLINT,                         -- owned by last-light-armory
    pvp_score  SMALLINT,                         -- owned by last-light-armory
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Which perks can roll on which weapon, and in which perk column (0-based).
CREATE TABLE weapon_perk (
    weapon_id    BIGINT NOT NULL REFERENCES weapon(id) ON DELETE CASCADE,
    perk_id      BIGINT NOT NULL REFERENCES perk(id) ON DELETE CASCADE,
    column_index SMALLINT NOT NULL,
    PRIMARY KEY (weapon_id, perk_id, column_index)
);

CREATE INDEX weapon_perk_perk_id_idx ON weapon_perk (perk_id);

-- One row per legal perk combination ("roll skeleton") for a weapon.
-- combo_key is a deterministic SHA-256 (hex) of the sorted
-- (column_index, perk_hash) pairs; it makes re-ingestion an upsert instead
-- of a duplicate-producing insert. Score columns owned by last-light-armory.
CREATE TABLE roll (
    id            BIGSERIAL PRIMARY KEY,
    weapon_id     BIGINT NOT NULL REFERENCES weapon(id) ON DELETE CASCADE,
    combo_key     TEXT NOT NULL,
    pve_score     NUMERIC(5,2),                  -- owned by last-light-armory
    pvp_score     NUMERIC(5,2),                  -- owned by last-light-armory
    overall_score NUMERIC(5,2),                  -- owned by last-light-armory
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (weapon_id, combo_key)
);

-- The perks making up a roll, one per perk column.
CREATE TABLE roll_perk (
    roll_id      BIGINT NOT NULL REFERENCES roll(id) ON DELETE CASCADE,
    perk_id      BIGINT NOT NULL REFERENCES perk(id),
    column_index SMALLINT NOT NULL,
    PRIMARY KEY (roll_id, column_index)
);

CREATE INDEX roll_perk_perk_id_idx ON roll_perk (perk_id);

-- Written exclusively by last-light-armory; created here because this repo
-- owns the schema.
CREATE TABLE weapon_ranking (
    weapon_id        BIGINT PRIMARY KEY REFERENCES weapon(id) ON DELETE CASCADE,
    overall_score    NUMERIC(5,2),
    pve_score        NUMERIC(5,2),
    pvp_score        NUMERIC(5,2),
    popularity_score NUMERIC(5,2),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
