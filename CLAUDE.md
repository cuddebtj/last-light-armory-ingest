# last-light-armory-ingest

_Last updated: 2026-07-05 — update this line whenever the file changes materially._

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

## Repo Relationship & Ownership Split (assumption — confirm this)

Both repos share one Postgres database. The split below is *my proposed
reading* of "this repo is focused solely on ingesting data" — not something
that was explicitly spelled out. Confirm or correct it before Milestone 5 gets
too far along, since it determines who's allowed to write to which columns.

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

- Server: `postgres.cuddelabs.com`
- Database: `last_light_armory` (already created — currently bare, only the
  `postgres` superuser exists)
- App role: `last_light_armory_admin` (**not yet created**)

### One-Time Setup (run manually via psql as the `postgres` superuser — this is *not* something the ingest binary does at runtime, and not something to automate into app startup)

```sql
CREATE ROLE last_light_armory_admin WITH LOGIN PASSWORD 'REPLACE_WITH_A_GENERATED_SECRET';
ALTER DATABASE last_light_armory OWNER TO last_light_armory_admin;
\c last_light_armory
GRANT ALL ON SCHEMA public TO last_light_armory_admin;
```

Making the role the database owner means it can create/alter tables via
migrations without needing per-object grants later. Generate the password with
a real secret generator, put it straight into `.env`, and never paste the
actual value into this file, a commit message, or a chat log.

Also worth checking, separately from the SQL above: whether `pg_hba.conf` /
any firewall in front of `postgres.cuddelabs.com` actually permits connections
from wherever this job runs (your dev machine, a CI runner, a scheduled server).
Creating the role doesn't guarantee the network path is open — that's a
separate thing to verify before assuming a connection failure is a code bug.

### `DATABASE_URL` format

```
postgresql://last_light_armory_admin:<password>@postgres.cuddelabs.com:5432/last_light_armory?sslmode=require
```

Port 5432 and `sslmode=require` are defaults, not confirmed against this
specific instance — check both against how the server's actually configured.

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

## Data Model (reference — implement as migrations)

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

## Open Decisions (resolve before the noted milestone — don't silently invent an answer and move on)

1. **Archetype derivation rule** — needed before Milestone 3. "Archetype"
   (e.g. "140rpm Hand Cannon") isn't a native Bungie field; it's RPM stat +
   weapon type + intrinsic frame. Don't guess bucket boundaries — pull real
   RPM distributions per weapon type from the manifest first, then set
   cutoffs from actual data, since buckets differ by weapon type.
2. **"Currently obtainable" compound rule** — needed before Milestone 2. No
   single Bungie flag covers this. Starting proposal: obtainable = (has an
   active `DestinyCollectibleDefinition` source not flagged unavailable) OR
   (craftable) OR (drops from a source Bungie currently marks active).
   Validate against known-vaulted and known-available weapons before trusting
   it at scale.
3. **Ownership split above** — confirm the this-repo-vs-last-light-armory
   table is actually how you want it divided; it's currently my inference,
   not something you stated outright.
4. **Migration tool & DB driver** — this doc assumes golang-migrate + pgx.
   Update this file if you pick differently.

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
go run ./cmd/ingest                                    # run a full check-and-import pass
go test ./...                                           # run all tests
migrate -path migrations -database "$DATABASE_URL" up   # apply pending migrations
```