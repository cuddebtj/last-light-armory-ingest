# last-light-armory-ingest

A Go batch job that pulls Destiny 2 weapon and perk data from the Bungie.net
API and loads it into the shared `last_light_armory` Postgres database. It
runs, does its work, and exits — it is not a web service, not the ranking
engine, and serves no HTTP traffic. Scoring, ranking, and the search UI live
in the sibling repo, **last-light-armory**.

## What one run does

1. Fetches `GET /Destiny2/Manifest/` and compares the `version` string with
   `manifest_sync_state.last_manifest_version`.
2. If unchanged (and `-force` not given): records the check and exits.
3. Otherwise downloads four definition tables in parallel
   (`DestinyInventoryItemDefinition`, `DestinyPlugSetDefinition`,
   `DestinyDamageTypeDefinition`, `DestinyCollectibleDefinition`), streaming
   the ~200 MB item table instead of buffering it.
4. Categorizes every weapon (type, frame, slot, element, fire-rate stat,
   tier, source, craftable/enhanceable/obtainable) across a CPU-bound worker
   pool.
5. Generates roll skeletons per weapon (see *Roll semantics* below).
6. Reconciles the database with idempotent upserts keyed on Bungie hashes:
   nothing is ever drop-and-reloaded, and rows curated by last-light-armory
   (perk/roll score columns, `weapon_ranking`) are never written.
7. Records the new version in `manifest_sync_state`.

A Postgres advisory lock guarantees only one ingest run mutates the database
at a time; concurrent starts fail fast instead of queueing.

## Roll semantics (decisions 2026-07-06)

- **Archetype is the weapon type** ("Auto Rifle", "Sniper Rifle", …). There
  is no separate archetype column; `weapon.weapon_type` is it. `frame` and
  `rpm` carry the rest of the identity (for bows/fusions/swords, `rpm`
  holds draw time / charge time / swing speed respectively).
- **`weapon_perk`** records the *full* perk pool: every rollable perk in
  every WEAPON PERKS column — barrels, magazines, traits, origin traits,
  enhanced variants included.
- **`roll`** rows are generated from **trait and origin columns only**
  (barrels/magazines never multiply combinations). Within a trait column,
  enhanced perk variants are preferred when the column has them; otherwise
  base traits are used, so non-enhanceable weapons and Exotics still get
  roll coverage. A weapon with no trait columns gets no rolls.
- Each roll's identity is `combo_key`: SHA-256 over its sorted
  (column, perk-hash) pairs. Re-ingestion prunes stale combos and inserts
  new ones; untouched rolls keep any scores last-light-armory wrote on them.
- **Obtainable heuristic v1**: a weapon is obtainable when it has a live
  collectible entry (present, not redacted/blacklisted) **or** is craftable.

## Layout

```
cmd/ingest/          entrypoint (flags, logging, wiring)
internal/bungie/     the only Bungie HTTP client: API key, User-Agent,
                     rate limit, retry/backoff, streaming JSON decoder
internal/config/     .env + environment loading and validation
internal/categorize/ pure derivation rules: weapon fields, perk columns
internal/rolls/      combination generation + combo keys
internal/db/         pgx pool, embedded migrations, advisory lock, repos
internal/ingest/     the pipeline orchestrator (parallel stages)
internal/models/     shared domain structs
migrations/          golang-migrate SQL, embedded into the binary
```

## Setup

Requirements: Go 1.26+, network access to bungie.net and the Postgres
server. No migrate CLI needed — the binary migrates the schema itself at
startup.

```sh
cp .env.example .env   # then fill in:
# BUNGIE_API_KEY  — Bungie.net app key (sent as X-API-Key)
# DATABASE_URL    — postgresql://last_light_armory_admin:<password>@postgres.cuddelabs.com:5432/last_light_armory
```

**Percent-encode special characters in the password** (`/` → `%2F`,
`@` → `%40`, `:` → `%3A`); an unencoded `/` makes the URL unparseable. The
`BUNGIE_OAUTH_*` variables in `.env.example` belong to the sibling repo and
are deliberately unused here — ingestion touches public manifest data only.

## Running

```sh
go run ./cmd/ingest                 # check version, import if changed
go run ./cmd/ingest -force          # import even if version unchanged
go run ./cmd/ingest -dry-run        # download + process, write nothing
go run ./cmd/ingest -verbose -json  # debug-level structured logs
```

Exit code 0 covers both "imported" and "unchanged, skipped"; 1 is any
failure. Logs go to stderr (text by default, `-json` for structured); a
one-line summary goes to stdout.

Destiny 2 is in maintenance mode (final content update June 2026), so a
weekly or even monthly cron is plenty:

```cron
# Mondays 09:00 — check manifest, import only if it changed
0 9 * * 1  cd /path/to/last-light-armory-ingest && ./ingest >> ingest.log 2>&1
```

## Testing

```sh
go test ./...                                  # unit tests (fast, offline)
go test -race ./...                            # with the race detector
go test -tags integration ./internal/db/       # against a real Postgres
```

Integration tests read `TEST_DATABASE_URL` (falling back to `DATABASE_URL`
/ the repo `.env`) and isolate each test in a throwaway schema
(`it_<timestamp>_<rand>`) selected via `search_path`, dropped on cleanup —
existing data in `public` is never touched. They verify migration
up/down/re-up cycles, upsert idempotency (a second identical run changes
zero rows), link/roll reconciliation, curated-score preservation across
re-ingest, transaction atomicity, and advisory-lock exclusion.

Combined coverage (unit + integration, `-coverpkg=./internal/...`):

```sh
go test -tags integration -coverprofile=cover.out -coverpkg=./internal/...,./migrations ./...
go tool cover -func=cover.out | tail -1
```

## Design notes

- **Parallelism**: definition downloads run concurrently (one goroutine per
  table, each writing only its own map); weapon categorization fans out over
  `NumCPU` workers feeding a single collector goroutine; database
  reconciliation fans out per weapon over a bounded pool. No shared mutable
  state is written concurrently — the race detector runs clean over the
  whole suite.
- **Idempotency**: every write is an `ON CONFLICT` upsert keyed on Bungie
  hash, with change detection (`IS DISTINCT FROM`) so no-op runs report
  `unchanged` instead of rewriting rows. Pruning is always scoped to a
  single weapon's links/rolls, never a table wipe.
- **Safety valves**: per-request retry with exponential backoff + jitter and
  `Retry-After` handling; a client-side rate limiter; a 1M-combination cap
  per weapon guards against pathological future manifests; migration and
  ingest both run under locks.
- **Secrets**: `.env` is gitignored; database URLs are never logged, and
  parse errors deliberately avoid echoing the URL (pgx's own errors include
  credentials).
