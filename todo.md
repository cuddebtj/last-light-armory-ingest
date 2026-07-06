# Roadmap: last-light-armory-ingest → production

_Written 2026-07-06. This file tracks what's left across both repos from
here to a deployed website. Update it as steps complete or plans change._

## Where things stand

- This repo (`last-light-armory-ingest`) implements Milestones 1–5 and is
  feature-complete: manifest sync, weapon/perk import, categorization, roll
  generation, icons, static JSON export. 98%+ test coverage, CI green,
  13 commits on `feat/ingest-pipeline`, **not yet pushed**.
- The database is live on the private dev server: 2,208 weapons, 1,057
  perks, 100,994 rolls, all scores still NULL (owned by the website repo).
- `data/` in this repo is a **regenerated build artifact** (gitignored) —
  run `go run ./cmd/export -out ./data` any time to refresh it locally.
  Currently populated: 47 MB, 2,208 weapon docs + index + perks.json + meta.json.
- The website repo (`last-light-armory`) does not exist yet.

## Phase 1 — Ship this branch (this repo)

- [ ] Push `feat/ingest-pipeline` and open a PR into `dev`
      (`git push -u origin feat/ingest-pipeline`, then `gh pr create`)
- [ ] Confirm CI passes on the PR (gofmt, vet, staticcheck, race tests)
- [ ] Merge to `dev`, tag a release if useful for reproducibility
      (e.g. `v0.1.0` — "milestones 1–5 complete")

## Phase 2 — Scaffold the website repo (`last-light-armory`)

- [ ] `gh repo create last-light-armory --public` (or private, your call)
- [ ] Next.js (App Router) + TypeScript + Tailwind, deployed to Vercel
- [ ] Decide on a `data/` layout matching this repo's export shape:
      `data/meta.json`, `data/perks.json`, `data/weapons/index.json`,
      `data/weapons/<hash>.json`
- [ ] Add a repo-level CLAUDE.md documenting the ownership split (mirror of
      this repo's — scores/rankings/votes live there, Bungie facts here)

## Phase 3 — Publish flow (connects the two repos)

This is the part that needs a real decision, not just code:

- [ ] Decide how exported JSON gets from this repo's private network into
      the public website repo. Options, roughly in order of simplicity:
      1. **Manual copy** — run `cmd/export`, `cp -r data/ ../last-light-armory/data/`,
         commit in the website repo. Fine for the current cadence (Destiny 2
         is in maintenance mode; re-exports are rare and driven by you).
      2. **Push script** — a `make publish` / `scripts/publish.sh` in this
         repo that runs ingest → export → copies into a sibling checkout of
         the website repo → commits + pushes there. Removes manual steps
         without needing any new infrastructure.
      3. **Private network sync job** — a cron on your network that does
         the above unattended after every ingest run. Worth it once you're
         not the one triggering re-exports by hand.
      Start with (1) or (2); only build (3) if re-exports become frequent
      (e.g. once the scoring/voting system is live and rankings update
      often).
- [ ] Whichever option: document the exact command sequence in this repo's
      README under "Serving the website" so it's not tribal knowledge.

## Phase 4 — Website MVP (last-light-armory repo)

- [ ] Weapon list page: client-side filter/search over `weapons/index.json`
      (2,208 entries — trivial in-browser, no server search needed)
- [ ] Weapon detail page: statically generated from `weapons/<hash>.json`
      (perk columns, rolls, icons via `https://www.bungie.net` + stored path)
- [ ] Perk name/icon lookups joined client-side from `perks.json`
- [ ] Basic SEO: static generation gives you this almost for free with
      Next.js `generateStaticParams` / metadata API
- [ ] Deploy to Vercel, confirm a production build works end-to-end from
      the committed `data/` artifacts

## Phase 5 — Scoring & ranking (Milestones 6–10, last-light-armory repo)

- [ ] Go batch job (mirrors this repo's shape) that writes `perk.pve_score`,
      `perk.pvp_score`, `roll.pve_score/pvp_score/overall_score`, and
      `weapon_ranking` — the columns this repo creates but only ever writes
      NULL into
- [ ] Scoring logic itself (formula, weighting) — genuinely undecided,
      needs its own design pass when you get there
- [ ] Re-export after every scoring run so the website picks up new numbers

## Phase 6 — Community voting (fantasy-football-style matchups)

Discussed 2026-07-06: present two weapons/rolls, user picks which they'd
run, aggregate into a popularity/consensus signal alongside the curated
scores.

- [ ] **New, separate data store** for votes — this is a write path, so it
      cannot be the private Postgres server or static JSON. Candidates:
      Vercel Postgres, Neon free tier, or Vercel KV for a simple tally.
      Small table: `(matchup_id, choice, voter_fingerprint, created_at)`.
- [ ] Next.js API route (or Server Action) to accept a vote — the only
      piece of this whole system that needs a public write endpoint
- [ ] Periodic job (on your network, like ingest) pulls vote tallies *in*
      from the public vote store, folds them into `weapon_ranking` or a new
      `popularity_score`, writes to the private DB
- [ ] Re-export after each pull so the site reflects updated popularity
- [ ] Basic abuse mitigation on the vote endpoint (rate limit by IP/fingerprint,
      no auth needed for v1 since there's no user account system yet)

## Deferred / revisit later

- **Managed hosting migration** — only if the project makes money (per
  CLAUDE.md decision #7); no action needed now.
- **User accounts / OAuth** — `BUNGIE_OAUTH_*` vars stay unused until a
  user-vault feature is actually scoped; don't wire them up speculatively.
- **"Currently obtainable" heuristic refinement** — v1 (collectible OR
  craftable) is implemented; revisit only if validation against known-vaulted
  weapons surfaces mismatches once the site is live and people notice.
