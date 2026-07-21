# last-light-armory-ingest

_Last updated: 2026-07-21 — update this line whenever the file changes materially._

## What This Repo Is

A Go batch job that pulls Destiny 2 weapon/perk data from the Bungie.net API and
loads it into the shared `last_light_armory` Postgres database. It is **not**
the web app, **not** the ranking engine, and **not** a service that serves
HTTP traffic to end users. It runs, does its work, and exits.

This repo implements Milestones 1–5 of the master project spec
("Destiny 2 Weapon Encyclopedia & Ranking Engine"):

1. Download the Bungie manifest
2. Import all currently obtainable weapons
3. Categorize by weapon type, frame, and archetype
4. Import all perk pools
5. Generate every legal perk combination (roll skeletons)

Milestones 6–10 (perk/roll scoring, ranking, search UI) belong to the sibling
repo, **last-light-armory**.

## Repo Relationship & Ownership Split (CONFIRMED 2026-07-06)

Both repos share one Postgres database. The split below was confirmed by the
owner on 2026-07-06.

| Concern | Owner | Notes |
|---|---|---|
| Schema / migrations | **this repo** | Ingest exists first and needs the tables anyway |
| `weapon`, `perk`, `weapon_perk` identity fields | **this repo** | Bungie-sourced facts only |
| `perk.pve_score` / `perk.pvp_score` | **last-light-armory** | Curated values, not from Bungie. This repo creates the columns but writes NULL, never a score. |
| `roll` generation (which perk combos exist) | **this repo** | Structural, not scored |
| `roll.pve_score` / `pvp_score` / `overall_score` | **last-light-armory** | Scoring logic lives there |
| `weapon_ranking` table | **last-light-armory** | Exclusively their writes |
| Search UI, HTTP API | **last-light-armory** | Out of scope here entirely |

## Non-Negotiables

- Bungie API is the only data source. Never scrape community sites (light.gg,
  DIM, D2Foundry, etc.), even for convenience or cross-checking.
- No OAuth, no user auth, no per-player vault/inventory data. Everything this
  repo touches is public manifest/definition data. If code here ever needs a
  non-empty `BUNGIE_OAUTH_*` value to function, that's a sign the design has
  drifted from scope — stop and reconsider before continuing.
- No scoring or ranking logic. If you catch yourself writing a formula that
  assigns a 0–10 or 0–100 value to a perk or roll, that logic belongs in
  last-light-armory, not here.
- All imports are idempotent upserts keyed on Bungie's `hash` values. Never a
  destructive drop-and-reload — hashes can occasionally be reissued or
  migrated across manifest versions, and re-running this job must always be
  safe.
- Never commit real credentials anywhere in this repo, including this file.
  Secrets live only in `.env` (gitignored) or a proper secret manager.
- Always use convetional commits and git branching strategies. **NEVER** commit
  to `main` or `dev`.

## Tech Stack

- **Go** — pin the exact version in `go.mod` to whatever `go version` reports
  locally; this doc assumes 1.22+ (generics, `log/slog`) but doesn't assert a
  specific current release.
- **Postgres 18** (already provisioned, see Database section)
- **pgx v5** (`jackc/pgx`) with `pgxpool` for connection pooling — preferred
  over `database/sql` + `lib/pq`, which is unmaintained
- **golang-migrate** for schema migrations (swap for `goose` if you'd rather —
  just update this doc so future sessions don't propose the other one out of
  habit)
- Standard library `testing`, table-driven tests

## Proposed Repo Layout

```
last-light-armory-ingest/
  cmd/
    ingest/
      main.go              // entrypoint: check manifest version, run import if changed
  internal/
    bungie/                // API client: manifest fetch, definition fetch, headers, retry/backoff
    db/                     // pgx pool setup, repositories, upsert helpers
    manifest/                // version tracking + diffing against manifest_sync_state
    categorize/              // weapon type / frame / archetype derivation rules
    rolls/                   // legal perk combination generation
    models/                  // shared structs (Weapon, Perk, Roll, etc.)
  migrations/                // golang-migrate SQL files, versioned
  .env.example
  .env                        // gitignored, never committed
  go.mod
  go.sum
  CLAUDE.md
```

## Environment Variables

From `.env.example`:

- `BUNGIE_API_KEY` — **required**. Sent as the `X-API-Key` header on every
  request. From the registered Bungie app (Private status).
- `BUNGIE_OAUTH_URL`, `BUNGIE_OAUTH_CLIENT_ID`, `BUNGIE_OAUTH_CLIENT_SECRET` —
  **not used by this repo.** Per the "no OAuth in v1" decision, ingestion only
  touches public data. These vars exist in the shared `.env.example`
  presumably for last-light-armory's future user-vault feature. Leave them
  blank here; don't wire them up just because the field exists.
- `DATABASE_URL` — Postgres connection string, see format below.

## Database

- Server: `postgres.cuddelabs.com` (Postgres 18.4)
- Database: `last_light_armory`
- App role: `last_light_armory_admin` (created 2026-07-06; has CREATE on the
  database — can create schemas — but no CREATEDB)

### `DATABASE_URL` format

```
postgresql://last_light_armory_admin:<password>@postgres.cuddelabs.com:5432/last_light_armory
```

**Percent-encode special characters in the password** (`/` → `%2F`,
`@` → `%40`, `:` → `%3A`). An unencoded `/` silently truncates the URL
authority and produces confusing "invalid port" errors. `internal/config`
detects this case and says so.

## Bungie API Notes

- Base URL: `https://www.bungie.net/Platform`
- Every request needs `X-API-Key: <BUNGIE_API_KEY>`. Nothing in this repo's
  scope needs an OAuth bearer token.
- Send a `User-Agent` header per Bungie's suggested format:
  `LastLightArmoryIngest/<version> AppId/<your-app-id> (+<repo-url>;<contact-email>)`
- `GET /Destiny2/Manifest/` returns a `version` string plus paths to the
  versioned definition tables (JSON or SQLite, by locale). That `version`
  string is the cheap "did anything change" check — compare it against
  `manifest_sync_state.last_manifest_version` before doing any real work.
- Definitions this project actually needs (names have been stable across
  Destiny 2's API history, but always sanity-check field shapes against a
  live manifest pull before assuming):
  - `DestinyInventoryItemDefinition` — weapons themselves: name, itemType /
    itemSubType, damage type refs, `sockets` (perk columns), `collectibleHash`,
    crafting-related fields
  - Socket category / socket type definitions — used to tell which socket
    entries are real perk columns vs. cosmetic/shader/mod slots
  - `DestinyPlugSetDefinition` — the actual perk options in a given
    reusable/random-roll socket; this is where "legal perk combination" data
    comes from
  - `DestinyStatDefinition` — stat hashes (RPM, range, stability, etc.),
    needed for archetype derivation
  - `DestinyDamageTypeDefinition` — elemental typing
  - `DestinyCollectibleDefinition` and related source data — feeds the
    "currently obtainable" determination
- No official hard rate-limit number is published, but limits are enforced.
  Keep the version-check job infrequent (see Sync Strategy) and don't
  parallel-hammer definition pulls.

## Sync Strategy

The original plan was a weekly Tuesday-reset cadence. That was a live-service
concept — vendor rotations and weekly resets. Destiny 2 shipped its final
content update (Monument of Triumph, June 9, 2026) and is now in maintenance
mode, so there's no weekly signal left to hook into. Instead:

- Check the manifest `version` string on a loose schedule — weekly or even
  monthly is enough. Daily is very likely overkill for a project that's
  explicitly evergreen, not live.
- Only run a full re-import if the version differs from
  `manifest_sync_state.last_manifest_version`.
- Initial pull and every subsequent recheck go through the exact same code
  path — no separate one-off "first run" script.
- All writes are upserts keyed on Bungie `hash` values, never destructive
  replace.

## Data Model

**Source of truth: `migrations/*.sql`** (embedded in the binary). The SQL
below is the original draft, kept for context; the implemented schema
differs in these ways:

- `weapon.archetype` dropped (archetype == weapon_type, decision #1)
- `weapon` gained `rpm INTEGER` and `tier TEXT`
- `perk` gained `enhanced BOOLEAN`
- `roll` gained `combo_key TEXT` with `UNIQUE (weapon_id, combo_key)` —
  without a natural key, re-ingestion would duplicate rolls
- indexes on `weapon_perk(perk_id)` and `roll_perk(perk_id)`
- `weapon` gained `icon TEXT`, `watermark TEXT` (migration 000003)
- `weapon` gained `ammo_type TEXT` and `breaker_type TEXT` (migration 000004,
  2026-07-21) — closes a data gap last-light-armory's CLAUDE.md flagged for
  its advanced-filtering work. Both are Bungie identity facts:
  `equippingBlock.ammoType` (DestinyAmmunitionType: 1 Primary, 2 Special, 3
  Heavy — every real weapon has a non-zero value, verified live against
  2,208/2,208 weapons) and `breakerType`/`breakerTypeHash` on
  `DestinyInventoryItemDefinition`, resolved via
  `DestinyBreakerTypeDefinition` (e.g. hash 485622768 → "Shield Piercing" =
  anti-Barrier). Confirmed live that **slot is not a valid ammo-type
  proxy**: Eriana's Vow is Special ammo in the Energy slot (Energy holds
  both primaries and specials). Only 17/2208 weapons have a non-null
  `breaker_type` — an intrinsic capability, not the common case; most
  champion-stun capability in this game comes from perks (Voltshot,
  Chill Clip, Incandescent, ...), which is curated verb knowledge that
  belongs in last-light-armory's scoring job, not here (see that repo's
  CLAUDE.md, "Champion/breaker capability: two distinct sources").
- `cmd/export`'s `weapons/index.json` entries gained a `columns` field
  (the same per-column perk-hash pool as the `<hash>.json` detail
  document) — previously only on the detail document, which meant
  client-side perk filtering needed every weapon's detail file just to
  know its perk pool. Verified live: adds roughly 3.3 MB raw to
  `index.json` (4.37 MB vs. an earlier ~1 MB estimate — pretty-printed
  JSON's indentation inflates raw size more than first assumed) but
  gzips to 432 KB, which is what actually crosses the wire.
- `cmd/export`'s `weapons/index.json`/`<hash>.json` entries gained
  `overall_score`/`pve_score`/`pvp_score`/`popularity_score` (2026-07-21,
  discovered as a real blocker for last-light-armory's task 18, not
  planned up front): `weapon_ranking` — owned and written exclusively by
  that repo's scoring job — previously had no read method or export field
  at all, so the website had zero weapon-level score to sort or rank on
  even though CLAUDE.md's own filtering-direction notes assumed one
  existed ("v1 may launch on weapon-level rank"). `AllWeaponRankings` is a
  plain join, not a gap: a weapon missing a ranking row (58/2208, all
  zero-roll weapons) simply doesn't appear in the read, and `Build` leaves
  those four fields nil rather than requiring every weapon to have one.
  `popularity_score` is Phase 6 (community voting) and always null for
  now — exported anyway so the shape doesn't need to change again later.
  Verified live: Fatebringer's exported `overall_score`/`pve_score`/
  `pvp_score` (42.83/45.31/40.36) match that repo's own hand-verified
  values for its top roll to the decimal; exactly 58 weapons came back
  with no ranking, matching that repo's own documented zero-roll count.

```sql
CREATE TABLE manifest_sync_state (
    id                    SMALLINT PRIMARY KEY DEFAULT 1,
    last_manifest_version TEXT NOT NULL,
    last_checked_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_changed_at       TIMESTAMPTZ,
    CHECK (id = 1)
);

CREATE TABLE weapon (
    id            BIGSERIAL PRIMARY KEY,
    hash          BIGINT UNIQUE NOT NULL,
    name          TEXT NOT NULL,
    weapon_type   TEXT NOT NULL,          -- e.g. "Auto Rifle"
    frame         TEXT,                    -- e.g. "Precision Frame" (native intrinsic perk name)
    archetype     TEXT,                    -- DERIVED — see Open Decisions #1, nullable until rule is set
    slot          TEXT NOT NULL,           -- Kinetic / Energy / Power
    element       TEXT,
    source        TEXT,
    craftable     BOOLEAN NOT NULL DEFAULT FALSE,
    enhanceable   BOOLEAN NOT NULL DEFAULT FALSE,
    obtainable    BOOLEAN NOT NULL DEFAULT FALSE,  -- DERIVED — see Open Decisions #2
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE perk (
    id          BIGSERIAL PRIMARY KEY,
    hash        BIGINT UNIQUE NOT NULL,
    name        TEXT NOT NULL,
    pve_score   SMALLINT,     -- curated by last-light-armory; this repo writes NULL only
    pvp_score   SMALLINT,     -- curated by last-light-armory; this repo writes NULL only
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE weapon_perk (
    weapon_id    BIGINT NOT NULL REFERENCES weapon(id) ON DELETE CASCADE,
    perk_id      BIGINT NOT NULL REFERENCES perk(id) ON DELETE CASCADE,
    column_index SMALLINT NOT NULL,
    PRIMARY KEY (weapon_id, perk_id, column_index)
);

CREATE TABLE roll (
    id            BIGSERIAL PRIMARY KEY,
    weapon_id     BIGINT NOT NULL REFERENCES weapon(id) ON DELETE CASCADE,
    pve_score     NUMERIC(5,2),   -- owned by last-light-armory
    pvp_score     NUMERIC(5,2),   -- owned by last-light-armory
    overall_score NUMERIC(5,2),   -- owned by last-light-armory
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Normalized replacement for the master spec's fixed perk1..perk5 columns.
-- Handles weapons with fewer/more real perk slots and enhanced-perk variants
-- without a schema migration later.
CREATE TABLE roll_perk (
    roll_id      BIGINT NOT NULL REFERENCES roll(id) ON DELETE CASCADE,
    perk_id      BIGINT NOT NULL REFERENCES perk(id),
    column_index SMALLINT NOT NULL,
    PRIMARY KEY (roll_id, column_index)
);

-- Written exclusively by last-light-armory, not by this repo.
CREATE TABLE weapon_ranking (
    weapon_id        BIGINT PRIMARY KEY REFERENCES weapon(id) ON DELETE CASCADE,
    overall_score    NUMERIC(5,2),
    pve_score        NUMERIC(5,2),
    pvp_score        NUMERIC(5,2),
    popularity_score NUMERIC(5,2),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Decisions (all resolved 2026-07-06 — the "Open Decisions" list is settled)

1. **Archetype IS the weapon type** ("Auto Rifle", "Sniper Rifle", …). The
   original "140rpm Hand Cannon"-style bucketing was a misstatement. The
   `weapon.archetype` column from the draft schema was dropped;
   `weapon_type` carries it, with `frame` and `rpm` alongside (for
   bows/fusions/swords, `rpm` stores draw time / charge time / swing speed).
2. **"Currently obtainable" heuristic v1**: obtainable = (has a
   `DestinyCollectibleDefinition` entry that exists and is neither redacted
   nor blacklisted) OR (craftable, i.e. `inventory.recipeItemHash` set).
   Implemented in `internal/categorize`; refine with an UPDATE + rule change
   there if validation against known-vaulted weapons demands it.
3. **Ownership split confirmed** as tabled above. All weapons are imported
   with the obtainable flag set accordingly (import-all-and-flag, not
   filter-at-import).
4. **golang-migrate + pgx v5 confirmed.** golang-migrate is used as a
   library with `go:embed`-ded SQL files — the binary migrates the schema
   itself at startup; no migrate CLI is installed or needed.
5. **Roll scope**: `roll` rows are combinations over trait + origin columns
   only (never barrels/mags — the full cartesian is ~1.2 billion rows).
   Within a trait column, enhanced perk variants are preferred when present,
   base traits otherwise, so Exotics and non-enhanceable weapons still get
   rolls. Identity = `combo_key` (SHA-256 of sorted column:perk pairs),
   unique per weapon. `weapon_perk` still records the full pool including
   barrels/mags/enhanced.
6. **Integration tests run against the shared dev server** (the DB is a dev
   environment). Tests isolate themselves in throwaway `it_*` schemas via
   `search_path` and drop them on cleanup — `public` is never touched.
   `TEST_DATABASE_URL` overrides the target. Run:
   `go test -tags integration ./internal/db/`.
7. **The database stays self-hosted and private** (decided 2026-07-06; may
   migrate to managed hosting if the project makes money). It is never
   exposed to the internet. The website consumes **static pre-baked JSON**
   from `cmd/export` — the only data that leaves the network. Consequences:
   the site can never query the DB live, GitHub Actions can't reach it (CI
   runs unit tests only; ingest/export run on the private network via cron),
   and score updates require a re-export.
8. **Website: Next.js on Vercel** (sibling repo last-light-armory), building
   from the committed export artifacts. Search/filtering happens client-side
   over the JSON — which is why the schema has no trigram/fulltext indexes.

## Coding Conventions

- Idiomatic Go: `gofmt`/`goimports` clean, `staticcheck` clean
- Table-driven tests especially for branching logic — archetype derivation
  and obtainability rules will keep needing edge-case tests as they evolve
- Structured logging via `log/slog` (standard library, no new dependency).
  Every run should log: manifest version checked, whether it changed, and
  counts of rows inserted / updated / unchanged
- All Bungie API calls go through one client wrapper in `internal/bungie` —
  don't scatter raw `http.Get` calls; this is the one place the API key
  header, User-Agent, and any retry/backoff logic live

## Commands

```
go run ./cmd/ingest                            # full check-and-import pass
go run ./cmd/ingest -dry-run                   # process but write nothing
go run ./cmd/ingest -force                     # import even if version unchanged
go test ./...                                  # unit tests (fast, offline)
go test -race ./...                            # with race detector
go test -tags integration ./internal/db/       # integration tests (dev Postgres)
```

Migrations apply automatically at binary startup (golang-migrate as a
library, embedded SQL) — there is no separate migrate CLI step.