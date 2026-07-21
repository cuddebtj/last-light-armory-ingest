// Package ingest orchestrates one full check-and-import pass:
//
//	manifest version check → definition downloads → weapon categorization →
//	roll generation → idempotent database writes → sync-state update.
//
// Concurrency model (race-free by construction):
//   - The four definition tables download in parallel, one goroutine each;
//     every goroutine builds its own private map, so nothing is shared while
//     writing (errgroup joins them before any map is read).
//   - Weapon processing fans out over a worker pool (WeaponWorkers); workers
//     read the shared lookup maps (read-only by then) and send results over
//     a channel to a single collector goroutine that owns all mutable state.
//   - Database reconciliation fans out per weapon (DBWorkers); each worker
//     touches disjoint rows (its own weapon's links/rolls), counts are
//     accumulated with atomics, and cross-run exclusion is guaranteed by the
//     Postgres advisory lock taken in main before Run is called.
package ingest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cuddebtj/last-light-armory-ingest/internal/bungie"
	"github.com/cuddebtj/last-light-armory-ingest/internal/categorize"
	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
	"github.com/cuddebtj/last-light-armory-ingest/internal/rolls"
)

// Definition tables this pipeline consumes.
const (
	componentInventoryItem = "DestinyInventoryItemDefinition"
	componentPlugSet       = "DestinyPlugSetDefinition"
	componentDamageType    = "DestinyDamageTypeDefinition"
	componentCollectible   = "DestinyCollectibleDefinition"
	componentBreakerType   = "DestinyBreakerTypeDefinition"
)

// ManifestAPI is the slice of the Bungie client the pipeline needs.
// *bungie.Client satisfies it.
type ManifestAPI interface {
	GetManifest(ctx context.Context) (*bungie.Manifest, error)
	DownloadComponent(ctx context.Context, path string) (io.ReadCloser, error)
}

// Storage is the slice of the database layer the pipeline needs.
// *db.Store satisfies it.
type Storage interface {
	GetSyncState(ctx context.Context) (version string, found bool, err error)
	UpsertSyncState(ctx context.Context, version string, changed bool) error
	UpsertPerks(ctx context.Context, perks []models.Perk) (db.Counts, error)
	UpsertWeapons(ctx context.Context, weapons []models.Weapon) (db.Counts, error)
	PerkIDsByHash(ctx context.Context) (map[int64]int64, error)
	ReplaceWeaponPerks(ctx context.Context, weaponHash int64, columns []models.PerkColumn) (inserted, deleted int64, err error)
	ReplaceRolls(ctx context.Context, weaponHash int64, rolls []db.Roll, perkIDs map[int64]int64) (inserted, deleted int64, err error)
}

// Runner wires the pipeline's dependencies and knobs.
type Runner struct {
	API   ManifestAPI
	Store Storage
	Log   *slog.Logger

	// Force imports even when the manifest version is unchanged.
	Force bool
	// DryRun stops after categorization and roll generation: nothing is
	// written to the database.
	DryRun bool
	// Locale selects the definition language; empty means "en".
	Locale string
	// WeaponWorkers sizes the CPU-bound categorization pool; 0 means
	// runtime.NumCPU().
	WeaponWorkers int
	// DBWorkers sizes the per-weapon database reconciliation pool; 0 means 4
	// (the pool holds 8 connections; leave headroom for the batch upserts).
	DBWorkers int
	// MaxRollsPerWeapon is a safety valve against pathological manifests:
	// weapons whose combination count exceeds it get no roll skeletons
	// (links still import). 0 means 1,000,000.
	MaxRollsPerWeapon int64
}

// Summary reports what one run did.
type Summary struct {
	ManifestVersion string
	Skipped         bool // version unchanged, nothing imported
	DryRun          bool

	WeaponsSeen  int
	Weapons      db.Counts
	Perks        db.Counts
	LinksAdded   int64
	LinksRemoved int64
	RollsSeen    int64
	RollsAdded   int64
	RollsRemoved int64
	Duration     time.Duration
}

// processed is one weapon's fully derived state, produced by a worker and
// consumed by the single collector.
type processed struct {
	weapon  models.Weapon
	columns []models.PerkColumn
	rolls   []db.Roll
}

// Run executes one check-and-import pass. The caller is responsible for
// holding the ingest advisory lock (see db.AcquireIngestLock).
func (r *Runner) Run(ctx context.Context) (*Summary, error) {
	start := time.Now()
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	locale := r.Locale
	if locale == "" {
		locale = "en"
	}

	manifest, err := r.API.GetManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	summary := &Summary{ManifestVersion: manifest.Version, DryRun: r.DryRun}

	prev, found, err := r.Store.GetSyncState(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}
	log.Info("manifest version checked",
		"version", manifest.Version, "previous", prev, "changed", !found || prev != manifest.Version)

	if found && prev == manifest.Version && !r.Force {
		if !r.DryRun {
			if err := r.Store.UpsertSyncState(ctx, manifest.Version, false); err != nil {
				return nil, fmt.Errorf("ingest: %w", err)
			}
		}
		summary.Skipped = true
		summary.Duration = time.Since(start)
		log.Info("manifest unchanged; skipping import", "version", manifest.Version)
		return summary, nil
	}

	lookups, weapons, err := r.download(ctx, manifest, locale, log)
	if err != nil {
		return nil, err
	}

	results, err := r.processWeapons(ctx, weapons, lookups, summary, log)
	if err != nil {
		return nil, err
	}

	if r.DryRun {
		summary.Duration = time.Since(start)
		log.Info("dry run complete; no writes performed",
			"weapons", summary.WeaponsSeen, "rolls", summary.RollsSeen)
		return summary, nil
	}

	if err := r.write(ctx, results, summary, log); err != nil {
		return nil, err
	}

	if err := r.Store.UpsertSyncState(ctx, manifest.Version, true); err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}

	summary.Duration = time.Since(start)
	log.Info("ingest complete",
		"version", manifest.Version,
		"weapons_inserted", summary.Weapons.Inserted,
		"weapons_updated", summary.Weapons.Updated,
		"weapons_unchanged", summary.Weapons.Unchanged,
		"perks_inserted", summary.Perks.Inserted,
		"perks_updated", summary.Perks.Updated,
		"perks_unchanged", summary.Perks.Unchanged,
		"links_added", summary.LinksAdded,
		"links_removed", summary.LinksRemoved,
		"rolls_added", summary.RollsAdded,
		"rolls_removed", summary.RollsRemoved,
		"duration", summary.Duration.Round(time.Millisecond),
	)
	return summary, nil
}

// download fetches the four definition tables in parallel. Each goroutine
// writes only to maps it owns; the errgroup Wait is the synchronization
// point after which everything is read-only.
func (r *Runner) download(ctx context.Context, manifest *bungie.Manifest, locale string, log *slog.Logger) (*categorize.Lookups, map[uint32]*bungie.InventoryItemDefinition, error) {
	paths := map[string]string{}
	for _, name := range []string{componentInventoryItem, componentPlugSet, componentDamageType, componentCollectible, componentBreakerType} {
		p, ok := manifest.ComponentPath(locale, name)
		if !ok {
			return nil, nil, fmt.Errorf("ingest: manifest has no %s path for locale %q", name, locale)
		}
		paths[name] = p
	}

	lookups := &categorize.Lookups{}
	weapons := map[uint32]*bungie.InventoryItemDefinition{}

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		m := map[uint32]string{}
		err := streamComponent(gctx, r.API, paths[componentDamageType], func(hash uint32, def bungie.DamageTypeDefinition) error {
			m[hash] = def.DisplayProperties.Name
			return nil
		})
		lookups.DamageTypes = m
		return err
	})
	g.Go(func() error {
		m := map[uint32]string{}
		err := streamComponent(gctx, r.API, paths[componentBreakerType], func(hash uint32, def bungie.BreakerTypeDefinition) error {
			m[hash] = def.DisplayProperties.Name
			return nil
		})
		lookups.BreakerTypes = m
		return err
	})
	g.Go(func() error {
		m := map[uint32]bungie.CollectibleDefinition{}
		err := streamComponent(gctx, r.API, paths[componentCollectible], func(hash uint32, def bungie.CollectibleDefinition) error {
			m[hash] = def
			return nil
		})
		lookups.Collectibles = m
		return err
	})
	g.Go(func() error {
		m := map[uint32]bungie.PlugSetDefinition{}
		err := streamComponent(gctx, r.API, paths[componentPlugSet], func(hash uint32, def bungie.PlugSetDefinition) error {
			m[hash] = def
			return nil
		})
		lookups.PlugSets = m
		return err
	})
	g.Go(func() error {
		// One pass over the ~200 MB item table: weapons kept whole, every
		// item kept minimally for perk/intrinsic name resolution.
		plugs := map[uint32]categorize.PlugInfo{}
		err := streamComponent(gctx, r.API, paths[componentInventoryItem], func(hash uint32, def bungie.InventoryItemDefinition) error {
			plugs[hash] = categorize.PlugInfo{
				Name:            def.DisplayProperties.Name,
				TypeDisplayName: def.ItemTypeDisplayName,
				Icon:            def.DisplayProperties.Icon,
			}
			if categorize.IsWeapon(&def) {
				d := def
				weapons[hash] = &d
			}
			return nil
		})
		lookups.Plugs = plugs
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, nil, fmt.Errorf("ingest: downloading definitions: %w", err)
	}
	log.Info("definitions downloaded",
		"weapons", len(weapons),
		"plug_sets", len(lookups.PlugSets),
		"collectibles", len(lookups.Collectibles),
		"damage_types", len(lookups.DamageTypes),
		"breaker_types", len(lookups.BreakerTypes),
	)
	return lookups, weapons, nil
}

// streamComponent downloads one definition table and streams it through fn.
// A free function because Go methods cannot have type parameters.
func streamComponent[T any](ctx context.Context, api ManifestAPI, path string, fn func(uint32, T) error) error {
	body, err := api.DownloadComponent(ctx, path)
	if err != nil {
		return err
	}
	defer body.Close()
	return bungie.StreamDefinitions(body, fn)
}

// processWeapons runs categorization and roll generation across a worker
// pool. Workers only read shared lookups; the collector goroutine is the
// sole writer of the aggregated results.
func (r *Runner) processWeapons(ctx context.Context, weapons map[uint32]*bungie.InventoryItemDefinition, lookups *categorize.Lookups, summary *Summary, log *slog.Logger) ([]processed, error) {
	workers := r.WeaponWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	maxRolls := r.MaxRollsPerWeapon
	if maxRolls <= 0 {
		maxRolls = 1_000_000
	}

	// Deterministic work order.
	hashes := make([]uint32, 0, len(weapons))
	for h := range weapons {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i] < hashes[j] })

	jobs := make(chan uint32)
	out := make(chan processed)

	// One errgroup owns feeder and workers: any failure cancels gctx, which
	// unblocks the feeder (so close(jobs) always happens) and the workers'
	// sends — no goroutine can deadlock waiting on a dead counterpart.
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobs)
		for _, h := range hashes {
			select {
			case jobs <- h:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		return nil
	})
	for i := 0; i < workers; i++ {
		g.Go(func() error {
			for h := range jobs {
				p := r.processOne(h, weapons[h], lookups, maxRolls, log)
				select {
				case out <- p:
				case <-gctx.Done():
					return gctx.Err()
				}
			}
			return nil
		})
	}
	go func() {
		g.Wait() // error retrieved by the g.Wait below; here we only sequence close(out)
		close(out)
	}()

	// The collector is the sole writer of results; it runs on this
	// goroutine, so no locking is needed.
	var results []processed
	var rollsSeen int64
	for p := range out {
		results = append(results, p)
		rollsSeen += int64(len(p.rolls))
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("ingest: processing weapons: %w", err)
	}

	// Deterministic result order for stable logs and writes.
	sort.Slice(results, func(i, j int) bool { return results[i].weapon.Hash < results[j].weapon.Hash })

	summary.WeaponsSeen = len(results)
	summary.RollsSeen = rollsSeen
	log.Info("weapons processed", "weapons", len(results), "rolls", rollsSeen)
	return results, nil
}

// processOne derives everything for a single weapon. Pure computation: no
// locks, no I/O, and no failure modes — categorize guarantees well-formed
// columns and the roll cap bounds generation.
func (r *Runner) processOne(hash uint32, def *bungie.InventoryItemDefinition, lookups *categorize.Lookups, maxRolls int64, log *slog.Logger) processed {
	columns := categorize.PerkColumns(def, lookups)
	weapon := categorize.Weapon(hash, def, lookups, columns)

	// Column indexes are ordinals assigned by categorize.PerkColumns, so
	// they are unique by construction — no duplicate-column validation is
	// needed before generation.
	rollColumns := categorize.RollColumns(columns)

	p := processed{weapon: weapon, columns: columns}
	if n := rolls.Count(rollColumns); n > maxRolls {
		log.Warn("skipping roll generation: combination count exceeds safety cap",
			"weapon", weapon.Name, "hash", hash, "combinations", n, "cap", maxRolls)
		return p
	}

	// The yield callback below cannot fail, so Generate cannot either.
	_ = rolls.Generate(rollColumns, func(combo []models.RollPerk) error {
		perks := make([]models.RollPerk, len(combo))
		copy(perks, combo)
		p.rolls = append(p.rolls, db.Roll{ComboKey: rolls.ComboKey(combo), Perks: perks})
		return nil
	})
	return p
}

// write persists everything: bulk perk/weapon upserts first (they satisfy
// the foreign keys), then per-weapon link and roll reconciliation across a
// bounded worker pool.
func (r *Runner) write(ctx context.Context, results []processed, summary *Summary, log *slog.Logger) error {
	// Deduplicate perks across weapons; sorted for deterministic batches.
	perkSet := map[int64]models.Perk{}
	for _, p := range results {
		for _, col := range p.columns {
			for _, perk := range col.Perks {
				perkSet[perk.Hash] = perk
			}
		}
	}
	allPerks := make([]models.Perk, 0, len(perkSet))
	for _, p := range perkSet {
		allPerks = append(allPerks, p)
	}
	sort.Slice(allPerks, func(i, j int) bool { return allPerks[i].Hash < allPerks[j].Hash })

	perkCounts, err := r.Store.UpsertPerks(ctx, allPerks)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}
	summary.Perks = perkCounts
	log.Info("perks upserted", "inserted", perkCounts.Inserted, "updated", perkCounts.Updated, "unchanged", perkCounts.Unchanged)

	allWeapons := make([]models.Weapon, len(results))
	for i, p := range results {
		allWeapons[i] = p.weapon
	}
	weaponCounts, err := r.Store.UpsertWeapons(ctx, allWeapons)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}
	summary.Weapons = weaponCounts
	log.Info("weapons upserted", "inserted", weaponCounts.Inserted, "updated", weaponCounts.Updated, "unchanged", weaponCounts.Unchanged)

	perkIDs, err := r.Store.PerkIDsByHash(ctx)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}

	dbWorkers := r.DBWorkers
	if dbWorkers <= 0 {
		dbWorkers = 4
	}

	var linksAdded, linksRemoved, rollsAdded, rollsRemoved atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(dbWorkers)
	for _, p := range results {
		g.Go(func() error {
			ins, del, err := r.Store.ReplaceWeaponPerks(gctx, p.weapon.Hash, p.columns)
			if err != nil {
				return err
			}
			linksAdded.Add(ins)
			linksRemoved.Add(del)

			rIns, rDel, err := r.Store.ReplaceRolls(gctx, p.weapon.Hash, p.rolls, perkIDs)
			if err != nil {
				return err
			}
			rollsAdded.Add(rIns)
			rollsRemoved.Add(rDel)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("ingest: reconciling links and rolls: %w", err)
	}

	summary.LinksAdded = linksAdded.Load()
	summary.LinksRemoved = linksRemoved.Load()
	summary.RollsAdded = rollsAdded.Load()
	summary.RollsRemoved = rollsRemoved.Load()
	return nil
}
