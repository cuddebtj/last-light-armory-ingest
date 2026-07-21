//go:build integration

// Integration tests against a real Postgres server (the shared dev instance
// by default — decision 2026-07-06). Each run isolates itself in a
// throwaway schema selected via search_path, so parallel work and real
// ingested data in `public` are never touched; the schema is dropped on
// cleanup.
//
// Run with:
//
//	go test -tags integration ./internal/db/
//
// Connection comes from TEST_DATABASE_URL, falling back to DATABASE_URL
// (loaded from the repo .env when present). Tests skip when neither is set.
package db_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cuddebtj/last-light-armory-ingest/internal/config"
	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// testEnv is the per-test database sandbox.
type testEnv struct {
	pool  *pgxpool.Pool // scoped to the throwaway schema
	url   string        // scoped URL (search_path pinned)
	store *db.Store
}

// baseURL resolves the server connection string, preferring
// TEST_DATABASE_URL, then DATABASE_URL, seeding from the repo .env.
func baseURL(t *testing.T) string {
	t.Helper()
	// Loads ../../.env if present; real environment always wins.
	_, _ = config.Load("../../.env")
	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		return u
	}
	if u := os.Getenv("DATABASE_URL"); u != "" {
		return u
	}
	t.Skip("integration tests need TEST_DATABASE_URL or DATABASE_URL")
	return ""
}

// scopedURL pins every connection made through it to the given schema.
func scopedURL(t *testing.T, base, schema string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	q := u.Query()
	q.Set("options", "-csearch_path="+schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// setup creates a fresh schema, migrates it, and registers cleanup that
// drops it again.
func setup(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	base := baseURL(t)

	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random schema suffix: %v", err)
	}
	schema := fmt.Sprintf("it_%d_%s", time.Now().Unix(), hex.EncodeToString(buf))

	admin, err := db.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting (admin): %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("creating schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	scoped := scopedURL(t, base, schema)
	if err := db.Migrate(scoped); err != nil {
		t.Fatalf("migrating schema %s: %v", schema, err)
	}

	pool, err := db.Connect(ctx, scoped)
	if err != nil {
		t.Fatalf("connecting (scoped): %v", err)
	}
	t.Cleanup(pool.Close)

	return &testEnv{pool: pool, url: scoped, store: db.NewStore(pool)}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// sampleWeapons returns two weapons, one fully populated and one sparse.
func sampleWeapons() []models.Weapon {
	return []models.Weapon{
		{
			Hash: 1000, Name: "Integration Rifle", WeaponType: "Auto Rifle",
			Frame: strPtr("Adaptive Frame"), RPM: intPtr(600), Slot: "Kinetic",
			Element: strPtr("Kinetic"), Tier: strPtr("Legendary"),
			Source: strPtr("Source: Integration testing."),
			Icon:   strPtr("/icons/rifle.jpg"), Watermark: strPtr("/icons/season.png"),
			AmmoType: strPtr("Primary"), BreakerType: strPtr("Shield Piercing"),
			Craftable: true, Enhanceable: true, Obtainable: true,
		},
		{Hash: 1001, Name: "Sparse Sword", WeaponType: "Sword", Slot: "Power"},
	}
}

func samplePerks() []models.Perk {
	return []models.Perk{
		{Hash: 30, Name: "Zen Moment", Icon: strPtr("/icons/zen.jpg")},
		{Hash: 31, Name: "Rampage"},
		{Hash: 32, Name: "Zen Moment", Enhanced: true},
		{Hash: 40, Name: "Veist Stinger"},
	}
}

func TestMigrateUpAndDown(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	// All tables exist in the scoped schema after Migrate (run by setup).
	for _, table := range []string{"manifest_sync_state", "weapon", "perk", "weapon_perk", "roll", "roll_perk", "weapon_ranking"} {
		var one int
		if err := env.pool.QueryRow(ctx, "SELECT 1 FROM "+table+" LIMIT 1").Scan(&one); err != nil && err.Error() != "no rows in result set" {
			t.Errorf("table %s not queryable: %v", table, err)
		}
	}

	if err := db.MigrateDown(env.url); err != nil {
		t.Fatalf("MigrateDown: %v", err)
	}
	var count int
	err := env.pool.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'weapon'`).Scan(&count)
	if err != nil {
		t.Fatalf("checking dropped tables: %v", err)
	}
	if count != 0 {
		t.Error("weapon table still exists after MigrateDown")
	}

	// Migrations must be re-appliable after a down (idempotent cycle).
	if err := db.Migrate(env.url); err != nil {
		t.Fatalf("re-Migrate after down: %v", err)
	}
}

func TestMigrateCreatesConsumptionIndexes(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	var count int
	err := env.pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname IN ('weapon_weapon_type_idx', 'weapon_obtainable_idx',
		                    'perk_name_idx', 'roll_weapon_score_idx')`).Scan(&count)
	if err != nil {
		t.Fatalf("querying pg_indexes: %v", err)
	}
	if count != 4 {
		t.Errorf("found %d of 4 expected consumption indexes", count)
	}
}

func TestSyncStateRoundTrip(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	if _, found, err := env.store.GetSyncState(ctx); err != nil || found {
		t.Fatalf("empty state: found=%v err=%v", found, err)
	}

	if err := env.store.UpsertSyncState(ctx, "v1", true); err != nil {
		t.Fatalf("UpsertSyncState v1: %v", err)
	}
	version, found, err := env.store.GetSyncState(ctx)
	if err != nil || !found || version != "v1" {
		t.Fatalf("after v1: version=%q found=%v err=%v", version, found, err)
	}

	var changedAt1 time.Time
	if err := env.pool.QueryRow(ctx, "SELECT last_changed_at FROM manifest_sync_state").Scan(&changedAt1); err != nil {
		t.Fatalf("reading last_changed_at: %v", err)
	}

	// An unchanged check bumps checked_at but must preserve changed_at.
	if err := env.store.UpsertSyncState(ctx, "v1", false); err != nil {
		t.Fatalf("UpsertSyncState unchanged: %v", err)
	}
	var changedAt2 time.Time
	if err := env.pool.QueryRow(ctx, "SELECT last_changed_at FROM manifest_sync_state").Scan(&changedAt2); err != nil {
		t.Fatalf("re-reading last_changed_at: %v", err)
	}
	if !changedAt1.Equal(changedAt2) {
		t.Errorf("last_changed_at moved on unchanged check: %v -> %v", changedAt1, changedAt2)
	}
}

func TestUpsertPerksIdempotent(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	perks := samplePerks()

	c, err := env.store.UpsertPerks(ctx, perks)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if c.Inserted != 4 || c.Updated != 0 || c.Unchanged != 0 {
		t.Errorf("first upsert counts = %+v", c)
	}

	// Identical re-run: everything unchanged (idempotency).
	c, err = env.store.UpsertPerks(ctx, perks)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if c.Inserted != 0 || c.Updated != 0 || c.Unchanged != 4 {
		t.Errorf("second upsert counts = %+v", c)
	}

	// A rename shows up as exactly one update.
	perks[1].Name = "Rampage (Renamed)"
	c, err = env.store.UpsertPerks(ctx, perks)
	if err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if c.Inserted != 0 || c.Updated != 1 || c.Unchanged != 3 {
		t.Errorf("third upsert counts = %+v", c)
	}

	// Curated score columns stay NULL — this repo must never write them.
	var pve, pvp *int16
	if err := env.pool.QueryRow(ctx, "SELECT pve_score, pvp_score FROM perk WHERE hash = 30").Scan(&pve, &pvp); err != nil {
		t.Fatalf("reading scores: %v", err)
	}
	if pve != nil || pvp != nil {
		t.Errorf("scores = %v/%v, want NULL/NULL", pve, pvp)
	}
}

func TestUpsertWeaponsIdempotentWithNulls(t *testing.T) {
	env := setup(t)
	ctx := context.Background()
	weapons := sampleWeapons()

	c, err := env.store.UpsertWeapons(ctx, weapons)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if c.Inserted != 2 {
		t.Errorf("counts = %+v", c)
	}

	c, err = env.store.UpsertWeapons(ctx, weapons)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if c.Unchanged != 2 || c.Inserted != 0 || c.Updated != 0 {
		t.Errorf("idempotent re-run counts = %+v", c)
	}

	var frame *string
	var rpm *int32
	if err := env.pool.QueryRow(ctx, "SELECT frame, rpm FROM weapon WHERE hash = 1001").Scan(&frame, &rpm); err != nil {
		t.Fatalf("reading sparse weapon: %v", err)
	}
	if frame != nil || rpm != nil {
		t.Errorf("sparse weapon frame/rpm = %v/%v, want NULL/NULL", frame, rpm)
	}

	// Obtainability flip is one update.
	weapons[1].Obtainable = true
	c, err = env.store.UpsertWeapons(ctx, weapons)
	if err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if c.Updated != 1 || c.Unchanged != 1 {
		t.Errorf("flip counts = %+v", c)
	}
}

func TestReplaceWeaponPerksReconciles(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	if _, err := env.store.UpsertWeapons(ctx, sampleWeapons()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.UpsertPerks(ctx, samplePerks()); err != nil {
		t.Fatal(err)
	}

	columns := []models.PerkColumn{
		{Index: 0, Perks: []models.Perk{{Hash: 30}, {Hash: 31}}},
		{Index: 1, Perks: []models.Perk{{Hash: 40}}},
	}
	ins, del, err := env.store.ReplaceWeaponPerks(ctx, 1000, columns)
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if ins != 3 || del != 0 {
		t.Errorf("first replace = +%d/-%d, want +3/-0", ins, del)
	}

	// Idempotent re-run.
	ins, del, err = env.store.ReplaceWeaponPerks(ctx, 1000, columns)
	if err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if ins != 0 || del != 0 {
		t.Errorf("idempotent replace = +%d/-%d, want +0/-0", ins, del)
	}

	// Drop one perk, add another: exactly one insert and one delete.
	columns[0].Perks = []models.Perk{{Hash: 30}, {Hash: 32}}
	ins, del, err = env.store.ReplaceWeaponPerks(ctx, 1000, columns)
	if err != nil {
		t.Fatalf("third replace: %v", err)
	}
	if ins != 1 || del != 1 {
		t.Errorf("reconcile = +%d/-%d, want +1/-1", ins, del)
	}
}

func TestReplaceRollsPreservesCuratedScores(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	if _, err := env.store.UpsertWeapons(ctx, sampleWeapons()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.UpsertPerks(ctx, samplePerks()); err != nil {
		t.Fatal(err)
	}
	perkIDs, err := env.store.PerkIDsByHash(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rolls := []db.Roll{
		{ComboKey: "combo-a", Perks: []models.RollPerk{{Column: 0, PerkHash: 30}, {Column: 1, PerkHash: 40}}},
		{ComboKey: "combo-b", Perks: []models.RollPerk{{Column: 0, PerkHash: 31}, {Column: 1, PerkHash: 40}}},
	}
	ins, del, err := env.store.ReplaceRolls(ctx, 1000, rolls, perkIDs)
	if err != nil {
		t.Fatalf("first ReplaceRolls: %v", err)
	}
	if ins != 2 || del != 0 {
		t.Errorf("first = +%d/-%d, want +2/-0", ins, del)
	}

	var rollPerkCount int
	if err := env.pool.QueryRow(ctx, "SELECT count(*) FROM roll_perk").Scan(&rollPerkCount); err != nil {
		t.Fatal(err)
	}
	if rollPerkCount != 4 {
		t.Errorf("roll_perk rows = %d, want 4", rollPerkCount)
	}

	// Simulate last-light-armory scoring a roll; re-ingest must not touch it.
	if _, err := env.pool.Exec(ctx,
		"UPDATE roll SET pve_score = 9.5 WHERE combo_key = 'combo-a'"); err != nil {
		t.Fatal(err)
	}

	ins, del, err = env.store.ReplaceRolls(ctx, 1000, rolls, perkIDs)
	if err != nil {
		t.Fatalf("idempotent ReplaceRolls: %v", err)
	}
	if ins != 0 || del != 0 {
		t.Errorf("idempotent = +%d/-%d, want +0/-0", ins, del)
	}
	var score *float64
	if err := env.pool.QueryRow(ctx, "SELECT pve_score FROM roll WHERE combo_key = 'combo-a'").Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score == nil || *score != 9.5 {
		t.Errorf("curated score = %v, want 9.5 preserved across re-ingest", score)
	}

	// combo-b retired, combo-c appears: prune + insert, combo-a untouched.
	rolls[1] = db.Roll{ComboKey: "combo-c", Perks: []models.RollPerk{{Column: 0, PerkHash: 32}, {Column: 1, PerkHash: 40}}}
	ins, del, err = env.store.ReplaceRolls(ctx, 1000, rolls, perkIDs)
	if err != nil {
		t.Fatalf("reconcile ReplaceRolls: %v", err)
	}
	if ins != 1 || del != 1 {
		t.Errorf("reconcile = +%d/-%d, want +1/-1", ins, del)
	}

	// Cascade: pruned roll's roll_perk rows are gone; totals stay consistent.
	if err := env.pool.QueryRow(ctx, "SELECT count(*) FROM roll_perk").Scan(&rollPerkCount); err != nil {
		t.Fatal(err)
	}
	if rollPerkCount != 4 {
		t.Errorf("roll_perk rows after reconcile = %d, want 4", rollPerkCount)
	}

	// Empty roll set prunes everything for the weapon.
	ins, del, err = env.store.ReplaceRolls(ctx, 1000, nil, perkIDs)
	if err != nil {
		t.Fatalf("empty ReplaceRolls: %v", err)
	}
	if ins != 0 || del != 2 {
		t.Errorf("empty = +%d/-%d, want +0/-2", ins, del)
	}
}

func TestReplaceRollsUnknownPerkFails(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	if _, err := env.store.UpsertWeapons(ctx, sampleWeapons()); err != nil {
		t.Fatal(err)
	}
	perkIDs := map[int64]int64{} // deliberately empty
	rolls := []db.Roll{{ComboKey: "x", Perks: []models.RollPerk{{Column: 0, PerkHash: 999}}}}
	if _, _, err := env.store.ReplaceRolls(ctx, 1000, rolls, perkIDs); err == nil {
		t.Fatal("want error for unknown perk hash, got nil")
	}
	// The failed transaction must not leave a partial roll behind.
	var count int
	if err := env.pool.QueryRow(ctx, "SELECT count(*) FROM roll").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rolls after failed tx = %d, want 0", count)
	}
}

func TestReadQueriesRoundTrip(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	// Seed through the same write paths ingest uses.
	if _, err := env.store.UpsertWeapons(ctx, sampleWeapons()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.UpsertPerks(ctx, samplePerks()); err != nil {
		t.Fatal(err)
	}
	columns := []models.PerkColumn{
		{Index: 0, Perks: []models.Perk{{Hash: 30}, {Hash: 31}}},
		{Index: 3, Perks: []models.Perk{{Hash: 40}}},
	}
	if _, _, err := env.store.ReplaceWeaponPerks(ctx, 1000, columns); err != nil {
		t.Fatal(err)
	}
	perkIDs, err := env.store.PerkIDsByHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolls := []db.Roll{
		{ComboKey: "bbb", Perks: []models.RollPerk{{Column: 0, PerkHash: 31}}},
		{ComboKey: "aaa", Perks: []models.RollPerk{{Column: 0, PerkHash: 30}, {Column: 3, PerkHash: 40}}},
	}
	if _, _, err := env.store.ReplaceRolls(ctx, 1000, rolls, perkIDs); err != nil {
		t.Fatal(err)
	}

	weapons, err := env.store.AllWeapons(ctx)
	if err != nil {
		t.Fatalf("AllWeapons: %v", err)
	}
	if len(weapons) != 2 || weapons[0].Hash != 1000 || weapons[1].Hash != 1001 {
		t.Errorf("weapons = %+v", weapons)
	}
	if weapons[0].Frame == nil || *weapons[0].Frame != "Adaptive Frame" || weapons[1].Frame != nil {
		t.Errorf("frames round-trip wrong: %+v", weapons)
	}
	if weapons[0].Icon == nil || *weapons[0].Icon != "/icons/rifle.jpg" || weapons[1].Icon != nil {
		t.Errorf("icons round-trip wrong: %+v", weapons)
	}
	if weapons[0].Watermark == nil || *weapons[0].Watermark != "/icons/season.png" {
		t.Errorf("watermark round-trip wrong: %v", weapons[0].Watermark)
	}
	if weapons[0].AmmoType == nil || *weapons[0].AmmoType != "Primary" || weapons[1].AmmoType != nil {
		t.Errorf("ammo_type round-trip wrong: %+v", weapons)
	}
	if weapons[0].BreakerType == nil || *weapons[0].BreakerType != "Shield Piercing" || weapons[1].BreakerType != nil {
		t.Errorf("breaker_type round-trip wrong: %+v", weapons)
	}

	perks, err := env.store.AllPerks(ctx)
	if err != nil {
		t.Fatalf("AllPerks: %v", err)
	}
	if len(perks) != 4 || perks[0].Hash != 30 || perks[0].PvEScore != nil {
		t.Errorf("perks = %+v", perks)
	}
	if perks[0].Icon == nil || *perks[0].Icon != "/icons/zen.jpg" || perks[1].Icon != nil {
		t.Errorf("perk icons round-trip wrong: %+v", perks[:2])
	}

	links, err := env.store.AllWeaponPerks(ctx)
	if err != nil {
		t.Fatalf("AllWeaponPerks: %v", err)
	}
	if len(links) != 3 || links[0] != (db.WeaponPerkRow{WeaponHash: 1000, ColumnIndex: 0, PerkHash: 30}) {
		t.Errorf("links = %+v", links)
	}

	rollRows, err := env.store.AllRollPerks(ctx)
	if err != nil {
		t.Fatalf("AllRollPerks: %v", err)
	}
	// Ordered by combo key: "aaa" (2 perks) before "bbb" (1 perk).
	if len(rollRows) != 3 || rollRows[0].ComboKey != "aaa" || rollRows[2].ComboKey != "bbb" {
		t.Errorf("rollRows = %+v", rollRows)
	}
	if rollRows[0].OverallScore != nil {
		t.Errorf("unscored roll has score: %+v", rollRows[0])
	}

	// weapon_ranking is written exclusively by last-light-armory's scoring
	// job, never by this repo, so there's no Store write method for it —
	// insert directly to exercise the read side of the ownership split.
	if _, err := env.pool.Exec(ctx,
		"INSERT INTO weapon_ranking (weapon_id, overall_score, pve_score, pvp_score) "+
			"SELECT id, 9.1, 9.5, 8.7 FROM weapon WHERE hash = 1000"); err != nil {
		t.Fatalf("seeding weapon_ranking: %v", err)
	}
	rankings, err := env.store.AllWeaponRankings(ctx)
	if err != nil {
		t.Fatalf("AllWeaponRankings: %v", err)
	}
	// Only weapon 1000 has a ranking row; weapon 1001 (zero rolls) never
	// gets one, same as production.
	if len(rankings) != 1 || rankings[0].WeaponHash != 1000 {
		t.Fatalf("rankings = %+v", rankings)
	}
	if rankings[0].OverallScore == nil || *rankings[0].OverallScore != 9.1 ||
		rankings[0].PvEScore == nil || *rankings[0].PvEScore != 9.5 ||
		rankings[0].PvPScore == nil || *rankings[0].PvPScore != 8.7 {
		t.Errorf("ranking round-trip wrong: %+v", rankings[0])
	}
	if rankings[0].PopularityScore != nil {
		t.Errorf("popularity_score = %v, want nil (phase 6 not started)", rankings[0].PopularityScore)
	}
}

func TestMigrateFailsOnDirtyState(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	// Simulate a migration that died mid-flight: golang-migrate marks the
	// version dirty and must refuse to proceed in either direction until a
	// human intervenes.
	if _, err := env.pool.Exec(ctx, "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("marking dirty: %v", err)
	}
	if err := db.Migrate(env.url); err == nil {
		t.Error("Migrate on dirty state must fail")
	}
	if err := db.MigrateDown(env.url); err == nil {
		t.Error("MigrateDown on dirty state must fail")
	}
}

func TestAcquireIngestLockClosedPool(t *testing.T) {
	env := setup(t)

	closed, err := db.Connect(context.Background(), env.url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	closed.Close()

	if _, err := db.AcquireIngestLock(context.Background(), closed); err == nil {
		t.Fatal("want error acquiring lock on closed pool")
	}
}

func TestAdvisoryLockExcludesConcurrentRuns(t *testing.T) {
	env := setup(t)
	ctx := context.Background()

	release, err := db.AcquireIngestLock(ctx, env.pool)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	// A second pool simulates another ingest process on the same server.
	other, err := db.Connect(ctx, env.url)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	defer other.Close()

	if _, err := db.AcquireIngestLock(ctx, other); err == nil {
		t.Fatal("second lock acquired concurrently; advisory lock failed")
	}

	release()
	release2, err := db.AcquireIngestLock(ctx, other)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	release2()
}
