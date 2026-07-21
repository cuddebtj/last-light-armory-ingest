package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuddebtj/last-light-armory-ingest/internal/bungie"
	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// ---- fake ManifestAPI ----

// fakeAPI serves an in-memory manifest and definition tables.
type fakeAPI struct {
	version     string
	components  map[string]string // component name -> JSON body
	manifestErr error
	downloadErr error
}

func (f *fakeAPI) GetManifest(ctx context.Context) (*bungie.Manifest, error) {
	if f.manifestErr != nil {
		return nil, f.manifestErr
	}
	paths := map[string]string{}
	for name := range f.components {
		paths[name] = "/defs/" + name
	}
	return &bungie.Manifest{
		Version:                        f.version,
		JSONWorldComponentContentPaths: map[string]map[string]string{"en": paths},
	}, nil
}

func (f *fakeAPI) DownloadComponent(ctx context.Context, path string) (io.ReadCloser, error) {
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	name := strings.TrimPrefix(path, "/defs/")
	body, ok := f.components[name]
	if !ok {
		return nil, fmt.Errorf("fake: no component %s", name)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// ---- fake Storage ----

// fakeStore records every write; a mutex makes it safe under the pipeline's
// concurrent reconciliation workers.
type fakeStore struct {
	mu sync.Mutex

	syncVersion string
	syncFound   bool

	perks       []models.Perk
	weapons     []models.Weapon
	linksByHash map[int64][]models.PerkColumn
	rollsByHash map[int64][]db.Roll
	syncUpserts []struct {
		Version string
		Changed bool
	}

	failOn string // method name that should return an error
}

var errInjected = errors.New("injected failure")

func (f *fakeStore) fail(method string) bool { return f.failOn == method }

func (f *fakeStore) GetSyncState(ctx context.Context) (string, bool, error) {
	if f.fail("GetSyncState") {
		return "", false, errInjected
	}
	return f.syncVersion, f.syncFound, nil
}

func (f *fakeStore) UpsertSyncState(ctx context.Context, version string, changed bool) error {
	if f.fail("UpsertSyncState") {
		return errInjected
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncUpserts = append(f.syncUpserts, struct {
		Version string
		Changed bool
	}{version, changed})
	return nil
}

func (f *fakeStore) UpsertPerks(ctx context.Context, perks []models.Perk) (db.Counts, error) {
	if f.fail("UpsertPerks") {
		return db.Counts{}, errInjected
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perks = perks
	return db.Counts{Inserted: len(perks)}, nil
}

func (f *fakeStore) UpsertWeapons(ctx context.Context, weapons []models.Weapon) (db.Counts, error) {
	if f.fail("UpsertWeapons") {
		return db.Counts{}, errInjected
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.weapons = weapons
	return db.Counts{Inserted: len(weapons)}, nil
}

func (f *fakeStore) PerkIDsByHash(ctx context.Context) (map[int64]int64, error) {
	if f.fail("PerkIDsByHash") {
		return nil, errInjected
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := map[int64]int64{}
	for i, p := range f.perks {
		ids[p.Hash] = int64(i + 1)
	}
	return ids, nil
}

func (f *fakeStore) ReplaceWeaponPerks(ctx context.Context, weaponHash int64, columns []models.PerkColumn) (int64, int64, error) {
	if f.fail("ReplaceWeaponPerks") {
		return 0, 0, errInjected
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.linksByHash == nil {
		f.linksByHash = map[int64][]models.PerkColumn{}
	}
	f.linksByHash[weaponHash] = columns
	n := int64(0)
	for _, c := range columns {
		n += int64(len(c.Perks))
	}
	return n, 0, nil
}

func (f *fakeStore) ReplaceRolls(ctx context.Context, weaponHash int64, rolls []db.Roll, perkIDs map[int64]int64) (int64, int64, error) {
	if f.fail("ReplaceRolls") {
		return 0, 0, errInjected
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rollsByHash == nil {
		f.rollsByHash = map[int64][]db.Roll{}
	}
	f.rollsByHash[weaponHash] = rolls
	return int64(len(rolls)), 0, nil
}

// ---- fixture JSON ----

// itemJSON builds a minimal weapon definition with one barrel column, two
// trait columns, and an origin column.
func weaponJSON(name string, subType int) string {
	return fmt.Sprintf(`{
		"displayProperties": {"name": %q, "icon": "/icons/weapon.jpg", "hasIcon": true},
		"iconWatermark": "/icons/season.png",
		"itemType": 3,
		"itemSubType": %d,
		"itemTypeDisplayName": "Auto Rifle",
		"inventory": {"tierTypeName": "Legendary", "bucketTypeHash": 1498876634, "recipeItemHash": 555},
		"collectibleHash": 900,
		"defaultDamageTypeHash": 3373582085,
		"stats": {"stats": {"4284893193": {"value": 600}}},
		"sockets": {
			"socketEntries": [
				{"singleInitialItemHash": 10},
				{"randomizedPlugSetHash": 100},
				{"randomizedPlugSetHash": 101},
				{"randomizedPlugSetHash": 102},
				{"reusablePlugSetHash": 103}
			],
			"socketCategories": [
				{"socketCategoryHash": 3956125808, "socketIndexes": [0]},
				{"socketCategoryHash": 4241085061, "socketIndexes": [1, 2, 3, 4]}
			]
		}
	}`, name, subType)
}

func plugJSON(name, typeName string) string {
	return fmt.Sprintf(`{"displayProperties":{"name":%q,"icon":"/icons/plug.jpg","hasIcon":true},"itemType":19,"itemTypeDisplayName":%q}`, name, typeName)
}

func plugSetJSON(entries ...[2]any) string {
	var items []string
	for _, e := range entries {
		items = append(items, fmt.Sprintf(`{"plugItemHash":%d,"currentlyCanRoll":%v}`, e[0], e[1]))
	}
	return fmt.Sprintf(`{"reusablePlugItems":[%s]}`, strings.Join(items, ","))
}

// newFixtureAPI returns a fakeAPI describing one weapon whose roll columns
// are 2 traits x 1 trait x 2 origins = 4 rolls.
func newFixtureAPI(version string) *fakeAPI {
	items := map[string]string{
		"1000": weaponJSON("Test Rifle", 6),
		"10":   plugJSON("Adaptive Frame", "Intrinsic"),
		"20":   plugJSON("Arrowhead Brake", "Barrel"),
		"30":   plugJSON("Zen Moment", "Trait"),
		"31":   plugJSON("Rampage", "Trait"),
		"32":   plugJSON("Kill Clip", "Trait"),
		"40":   plugJSON("Veist Stinger", "Origin Trait"),
		"41":   plugJSON("Hakke Breach", "Origin Trait"),
	}
	var itemPairs []string
	for h, j := range items {
		itemPairs = append(itemPairs, fmt.Sprintf("%q:%s", h, j))
	}
	return &fakeAPI{
		version: version,
		components: map[string]string{
			componentInventoryItem: "{" + strings.Join(itemPairs, ",") + "}",
			componentPlugSet: fmt.Sprintf(`{
				"100": %s,
				"101": %s,
				"102": %s,
				"103": %s
			}`,
				plugSetJSON([2]any{20, true}),
				plugSetJSON([2]any{30, true}, [2]any{31, true}),
				plugSetJSON([2]any{32, true}),
				plugSetJSON([2]any{40, true}, [2]any{41, true})),
			componentDamageType:  `{"3373582085": {"displayProperties": {"name": "Kinetic"}}}`,
			componentCollectible: `{"900": {"sourceString": "Source: Testing."}}`,
			componentBreakerType: `{"485622768": {"displayProperties": {"name": "Shield Piercing"}}}`,
		},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- tests ----

func TestRunFullImport(t *testing.T) {
	api := newFixtureAPI("v-new")
	store := &fakeStore{}
	r := &Runner{API: api, Store: store, Log: testLogger(), WeaponWorkers: 4, DBWorkers: 4}

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if s.Skipped {
		t.Error("Skipped = true, want false")
	}
	if s.ManifestVersion != "v-new" {
		t.Errorf("ManifestVersion = %q", s.ManifestVersion)
	}
	if s.WeaponsSeen != 1 || len(store.weapons) != 1 {
		t.Fatalf("WeaponsSeen = %d, stored %d", s.WeaponsSeen, len(store.weapons))
	}

	w := store.weapons[0]
	if w.Name != "Test Rifle" || w.Hash != 1000 || !w.Craftable || !w.Obtainable {
		t.Errorf("weapon = %+v", w)
	}
	if w.Frame == nil || *w.Frame != "Adaptive Frame" {
		t.Errorf("Frame = %v", w.Frame)
	}
	if w.Icon == nil || *w.Icon != "/icons/weapon.jpg" {
		t.Errorf("Icon = %v", w.Icon)
	}
	if w.Watermark == nil || *w.Watermark != "/icons/season.png" {
		t.Errorf("Watermark = %v", w.Watermark)
	}

	// Perk icons flow from the plug definitions into the stored pool.
	for _, p := range store.perks {
		if p.Icon == nil || *p.Icon != "/icons/plug.jpg" {
			t.Errorf("perk %d icon = %v", p.Hash, p.Icon)
		}
	}

	// Perk pool: barrel + 3 traits + 2 origins = 6 perks (intrinsic is not a column perk).
	if len(store.perks) != 6 {
		t.Errorf("perks stored = %d, want 6: %+v", len(store.perks), store.perks)
	}

	// Rolls: (Zen|Rampage) x KillClip x (Veist|Hakke) = 4.
	if s.RollsSeen != 4 {
		t.Errorf("RollsSeen = %d, want 4", s.RollsSeen)
	}
	gotRolls := store.rollsByHash[1000]
	if len(gotRolls) != 4 {
		t.Fatalf("stored rolls = %d, want 4", len(gotRolls))
	}
	keys := map[string]bool{}
	for _, roll := range gotRolls {
		if len(roll.Perks) != 3 {
			t.Errorf("roll has %d perks, want 3: %+v", len(roll.Perks), roll)
		}
		keys[roll.ComboKey] = true
	}
	if len(keys) != 4 {
		t.Errorf("combo keys not unique: %v", keys)
	}

	// Sync state recorded as changed.
	if len(store.syncUpserts) != 1 || !store.syncUpserts[0].Changed || store.syncUpserts[0].Version != "v-new" {
		t.Errorf("syncUpserts = %+v", store.syncUpserts)
	}
}

func TestRunSkipsWhenVersionUnchanged(t *testing.T) {
	api := newFixtureAPI("v-same")
	store := &fakeStore{syncVersion: "v-same", syncFound: true}
	r := &Runner{API: api, Store: store, Log: testLogger()}

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.Skipped {
		t.Fatal("Skipped = false, want true")
	}
	if len(store.weapons) != 0 || len(store.perks) != 0 {
		t.Error("skip path must not write weapons/perks")
	}
	// last_checked_at still recorded, as an unchanged check.
	if len(store.syncUpserts) != 1 || store.syncUpserts[0].Changed {
		t.Errorf("syncUpserts = %+v, want one unchanged record", store.syncUpserts)
	}
}

func TestRunForceOverridesUnchangedVersion(t *testing.T) {
	api := newFixtureAPI("v-same")
	store := &fakeStore{syncVersion: "v-same", syncFound: true}
	r := &Runner{API: api, Store: store, Log: testLogger(), Force: true}

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.Skipped {
		t.Fatal("Skipped = true; Force must import anyway")
	}
	if len(store.weapons) != 1 {
		t.Errorf("weapons stored = %d, want 1", len(store.weapons))
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	api := newFixtureAPI("v-new")
	store := &fakeStore{}
	r := &Runner{API: api, Store: store, Log: testLogger(), DryRun: true}

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.DryRun || s.WeaponsSeen != 1 || s.RollsSeen != 4 {
		t.Errorf("summary = %+v", s)
	}
	if len(store.weapons) != 0 || len(store.perks) != 0 || len(store.syncUpserts) != 0 {
		t.Error("dry run must not write anything")
	}
}

func TestRunRollCapSkipsGeneration(t *testing.T) {
	api := newFixtureAPI("v-new")
	store := &fakeStore{}
	r := &Runner{API: api, Store: store, Log: testLogger(), MaxRollsPerWeapon: 3} // fixture generates 4

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.RollsSeen != 0 {
		t.Errorf("RollsSeen = %d, want 0 (capped)", s.RollsSeen)
	}
	if len(store.weapons) != 1 {
		t.Error("capped weapon must still be imported")
	}
	if got := store.rollsByHash[1000]; len(got) != 0 {
		t.Errorf("rolls stored despite cap: %+v", got)
	}
}

func TestRunErrorPropagation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeAPI, *fakeStore)
	}{
		{"manifest fetch fails", func(a *fakeAPI, s *fakeStore) { a.manifestErr = errInjected }},
		{"download fails", func(a *fakeAPI, s *fakeStore) { a.downloadErr = errInjected }},
		{"sync state read fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "GetSyncState" }},
		{"perk upsert fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "UpsertPerks" }},
		{"weapon upsert fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "UpsertWeapons" }},
		{"perk id load fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "PerkIDsByHash" }},
		{"link replace fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "ReplaceWeaponPerks" }},
		{"roll replace fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "ReplaceRolls" }},
		{"final sync upsert fails", func(a *fakeAPI, s *fakeStore) { s.failOn = "UpsertSyncState" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFixtureAPI("v-new")
			store := &fakeStore{}
			tt.mutate(api, store)
			r := &Runner{API: api, Store: store, Log: testLogger()}
			if _, err := r.Run(context.Background()); !errors.Is(err, errInjected) {
				t.Fatalf("want injected error, got %v", err)
			}
		})
	}
}

func TestRunMissingComponentPath(t *testing.T) {
	api := newFixtureAPI("v-new")
	delete(api.components, componentPlugSet)
	r := &Runner{API: api, Store: &fakeStore{}, Log: testLogger()}
	_, err := r.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), componentPlugSet) {
		t.Fatalf("want missing-component error, got %v", err)
	}
}

// TestRunManyWeaponsConcurrently exercises the worker pools with enough
// weapons to make scheduling interesting; run with -race this verifies the
// pipeline's no-shared-mutable-state claims.
func TestRunManyWeaponsConcurrently(t *testing.T) {
	api := newFixtureAPI("v-new")

	// Rebuild the item table with 200 weapons sharing the plug fixtures.
	items := map[string]string{
		"10": plugJSON("Adaptive Frame", "Intrinsic"),
		"20": plugJSON("Arrowhead Brake", "Barrel"),
		"30": plugJSON("Zen Moment", "Trait"),
		"31": plugJSON("Rampage", "Trait"),
		"32": plugJSON("Kill Clip", "Trait"),
		"40": plugJSON("Veist Stinger", "Origin Trait"),
		"41": plugJSON("Hakke Breach", "Origin Trait"),
	}
	for i := 0; i < 200; i++ {
		items[fmt.Sprint(5000+i)] = weaponJSON(fmt.Sprintf("Rifle %03d", i), 6)
	}
	var pairs []string
	for h, j := range items {
		pairs = append(pairs, fmt.Sprintf("%q:%s", h, j))
	}
	api.components[componentInventoryItem] = "{" + strings.Join(pairs, ",") + "}"

	store := &fakeStore{}
	r := &Runner{API: api, Store: store, Log: testLogger(), WeaponWorkers: 8, DBWorkers: 8}

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.WeaponsSeen != 200 {
		t.Errorf("WeaponsSeen = %d, want 200", s.WeaponsSeen)
	}
	if s.RollsSeen != 800 {
		t.Errorf("RollsSeen = %d, want 800", s.RollsSeen)
	}
	if len(store.rollsByHash) != 200 {
		t.Errorf("rollsByHash has %d weapons, want 200", len(store.rollsByHash))
	}
	// Weapons must arrive at the store sorted by hash (deterministic writes).
	for i := 1; i < len(store.weapons); i++ {
		if store.weapons[i-1].Hash >= store.weapons[i].Hash {
			t.Fatalf("weapons not sorted at %d: %d >= %d", i, store.weapons[i-1].Hash, store.weapons[i].Hash)
		}
	}
}

// blockingBody is an io.ReadCloser whose Read blocks until the context is
// cancelled, then surfaces the context error.
type blockingBody struct{ ctx context.Context }

func (b blockingBody) Read(p []byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b blockingBody) Close() error { return nil }

// blockingAPI hangs every component download until the context dies.
type blockingAPI struct{ inner *fakeAPI }

func (b *blockingAPI) GetManifest(ctx context.Context) (*bungie.Manifest, error) {
	return b.inner.GetManifest(ctx)
}

func (b *blockingAPI) DownloadComponent(ctx context.Context, path string) (io.ReadCloser, error) {
	return blockingBody{ctx: ctx}, nil
}

func TestRunCancellationUnblocksPipeline(t *testing.T) {
	// Downloads hang forever; cancelling the context must unwind every
	// stage promptly instead of deadlocking. Deterministic: Run cannot
	// succeed, and the 5s guard converts a hang into a failure.
	api := &blockingAPI{inner: newFixtureAPI("v-new")}
	r := &Runner{API: api, Store: &fakeStore{}, Log: testLogger(), WeaponWorkers: 2}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx)
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want error from cancelled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not unwind within 5s of cancellation: pipeline deadlock")
	}
}
