-- 000003_icons.up.sql
-- Icon CDN paths for the website (relative to https://www.bungie.net).
-- watermark is the season/expansion badge overlaid on weapon icons; every
-- weapon in the current manifest has both fields (verified live 2026-07-06).

BEGIN;

ALTER TABLE weapon ADD COLUMN icon TEXT;
ALTER TABLE weapon ADD COLUMN watermark TEXT;
ALTER TABLE perk   ADD COLUMN icon TEXT;

COMMIT;
