# Contributing

Thanks for looking at last-light-armory-ingest. This repo is a pure
Bungie-data mirror — see [`README.md`](README.md) for what it does and how
it fits with the sibling [`last-light-armory`][product] repo (scoring,
ranking, and the website all live there, not here).

## Non-negotiables

These aren't style preferences — they're the reason this repo exists as a
separate thing from `last-light-armory`. A PR that crosses one of these
needs a real conversation first, not just a review comment:

- **Bungie's API is the only data source.** Never scrape community sites
  (light.gg, DIM, D2Foundry, ...), even for convenience or cross-checking.
- **No OAuth, no per-player data.** Everything here is public manifest
  data. If you find yourself needing a non-empty `BUNGIE_OAUTH_*` value,
  the design has drifted from scope — stop and reconsider.
- **No scoring or ranking logic.** If you catch yourself writing a formula
  that assigns a quality score to a perk or roll, that belongs in
  `last-light-armory`, not here.
- **All imports are idempotent upserts** keyed on Bungie's `hash` values.
  Never a destructive drop-and-reload.
- **Never commit real credentials.** Secrets live only in `.env`
  (gitignored) or a real secret manager.
- **Never commit directly to `main` or `dev`.** Branch off `dev`, open a
  PR back into it.

## Branching and commits

- Branch names are `type/short-description` (`feat/...`, `fix/...`,
  `chore/...`, `docs/...`).
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/):
  `type: summary` or `type(scope): summary`, e.g.
  `feat: stream the item table instead of buffering it`. The summary
  describes *why*, not a restatement of the diff.

## Setup

```sh
cp .env.example .env
# BUNGIE_API_KEY — from your registered Bungie.net app (Private status)
# DATABASE_URL   — the shared Postgres connection string
go run ./cmd/ingest -dry-run   # sanity-check your setup without writing anything
```

Full setup details, all commands, and the data model are in
[`README.md`](README.md).

## What a PR needs to pass

CI (`.github/workflows/ci.yml`) gates on:

```sh
gofmt -l .          # must produce no output
go vet ./...
staticcheck ./...
go test -race ./...
go build ./...
```

Integration tests are **not** run in CI — they need the private-network
Postgres server, which GitHub's runners can't reach. Run them yourself
before opening a PR that touches `internal/db/` or any migration:

```sh
go test -tags integration ./internal/db/
```

They isolate themselves in a throwaway schema (`it_<timestamp>_<rand>`)
and drop it on cleanup; your `.env`'s `DATABASE_URL` (or `TEST_DATABASE_URL`
if you want to point somewhere else) is never at risk of a destructive
write to real data.

New code lands with its own tests in the same PR, table-driven wherever
there's real branching to cover — that's especially true for
`internal/categorize`'s derivation rules and `internal/rolls`'s
combination generation, both of which keep needing edge-case coverage as
the manifest evolves.

## Verifying a change actually works

Passing tests isn't the same as verifying a real run works. Before opening
a PR that touches the pipeline itself (not just a pure helper function),
run it for real against a database you have access to:

```sh
go run ./cmd/ingest -dry-run     # confirm it downloads/categorizes without error
go run ./cmd/ingest -force       # confirm a real write, then re-run it
go run ./cmd/ingest -force       # and confirm the second run reports "unchanged", not duplicate writes
```

That two-run idempotency check is the actual bar this project holds
itself to — see the git history for examples of a change verified this
way (row counts, bounds checks, hand-computed spot checks) before being
trusted, not just a passing `go test`.

## Code style

Idiomatic Go: `gofmt`-clean, `go vet`-clean, `staticcheck`-clean. Doc
comments on every exported identifier. All Bungie API calls go through
`internal/bungie`'s one client wrapper — never a raw `http.Get` scattered
elsewhere; that's the one place the API key header, User-Agent, and
retry/backoff logic live. Structured logging via `log/slog` (standard
library only); every run should log the manifest version checked, whether
it changed, and insert/update/unchanged counts.

[product]: https://github.com/cuddebtj/last-light-armory
