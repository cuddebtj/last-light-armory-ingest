package categorize

import (
	"strconv"
	"testing"

	"github.com/cuddebtj/last-light-armory-ingest/internal/bungie"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// defBuilder assembles a synthetic InventoryItemDefinition without drowning
// tests in struct literals.
type defBuilder struct {
	def bungie.InventoryItemDefinition
}

func newWeaponDef(name string) *defBuilder {
	b := &defBuilder{}
	b.def.ItemType = bungie.ItemTypeWeapon
	b.def.DisplayProperties.Name = name
	b.def.ItemTypeDisplayName = "Auto Rifle"
	b.def.ItemSubType = 6
	b.def.Inventory.BucketTypeHash = bungie.BucketKinetic
	b.def.Inventory.TierTypeName = "Legendary"
	return b
}

func (b *defBuilder) stat(hash uint32, value int) *defBuilder {
	if b.def.Stats.Stats == nil {
		b.def.Stats.Stats = map[string]struct {
			Value int `json:"value"`
		}{}
	}
	b.def.Stats.Stats[strconv.FormatUint(uint64(hash), 10)] = struct {
		Value int `json:"value"`
	}{value}
	return b
}

// socket appends a socket entry and registers it under the given category.
func (b *defBuilder) socket(category uint32, entry bungie.SocketEntry) *defBuilder {
	b.def.Sockets.SocketEntries = append(b.def.Sockets.SocketEntries, entry)
	idx := len(b.def.Sockets.SocketEntries) - 1
	for i, cat := range b.def.Sockets.SocketCategories {
		if cat.SocketCategoryHash == category {
			b.def.Sockets.SocketCategories[i].SocketIndexes = append(cat.SocketIndexes, idx)
			return b
		}
	}
	b.def.Sockets.SocketCategories = append(b.def.Sockets.SocketCategories, struct {
		SocketCategoryHash uint32 `json:"socketCategoryHash"`
		SocketIndexes      []int  `json:"socketIndexes"`
	}{category, []int{idx}})
	return b
}

// plugSet builds a PlugSetDefinition from (hash, currentlyCanRoll) pairs.
func plugSet(plugs ...[2]uint32) bungie.PlugSetDefinition {
	var ps bungie.PlugSetDefinition
	for _, p := range plugs {
		ps.ReusablePlugItems = append(ps.ReusablePlugItems, struct {
			PlugItemHash     uint32 `json:"plugItemHash"`
			CurrentlyCanRoll bool   `json:"currentlyCanRoll"`
		}{p[0], p[1] == 1})
	}
	return ps
}

// baseLookups covers the plug/damage/collectible tables tests share.
func baseLookups() *Lookups {
	return &Lookups{
		DamageTypes: map[uint32]string{
			3373582085: "Kinetic",
			2303181850: "Arc",
		},
		Collectibles: map[uint32]bungie.CollectibleDefinition{
			900: {SourceString: "Source: The Vault of Tests."},
			901: {Redacted: true},
			902: {Blacklisted: true},
		},
		PlugSets: map[uint32]bungie.PlugSetDefinition{},
		Plugs: map[uint32]PlugInfo{
			10: {Name: "Adaptive Frame", TypeDisplayName: "Intrinsic"},
			20: {Name: "Arrowhead Brake", TypeDisplayName: "Barrel"},
			21: {Name: "Chambered Compensator", TypeDisplayName: "Barrel"},
			30: {Name: "Zen Moment", TypeDisplayName: "Trait", Icon: "/common/icons/zen.jpg"},
			31: {Name: "Rampage", TypeDisplayName: "Trait"},
			32: {Name: "Zen Moment", TypeDisplayName: "Enhanced Trait"},
			33: {Name: "Rampage", TypeDisplayName: "Enhanced Trait"},
			40: {Name: "Veist Stinger", TypeDisplayName: "Origin Trait"},
			41: {Name: "Hakke Breach Armaments", TypeDisplayName: "Origin Trait"},
			50: {Name: "", TypeDisplayName: "Trait"}, // unnamed: must be skipped
		},
	}
}

func TestIsWeapon(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*bungie.InventoryItemDefinition)
		want bool
	}{
		{"plain weapon", func(d *bungie.InventoryItemDefinition) {}, true},
		{"not a weapon", func(d *bungie.InventoryItemDefinition) { d.ItemType = 20 }, false},
		{"redacted", func(d *bungie.InventoryItemDefinition) { d.Redacted = true }, false},
		{"blacklisted", func(d *bungie.InventoryItemDefinition) { d.Blacklisted = true }, false},
		{"nameless", func(d *bungie.InventoryItemDefinition) { d.DisplayProperties.Name = "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := newWeaponDef("Test-A").def
			tt.mod(&def)
			if got := IsWeapon(&def); got != tt.want {
				t.Errorf("IsWeapon = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWeaponFieldDerivation(t *testing.T) {
	lk := baseLookups()
	b := newWeaponDef("The Forward Path").
		stat(bungie.StatRoundsPerMinute, 600).
		socket(bungie.SocketCategoryIntrinsic, bungie.SocketEntry{SingleInitialItemHash: 10})
	b.def.DefaultDamageTypeHash = 3373582085
	b.def.CollectibleHash = 900
	b.def.DisplayProperties.Icon = "/common/icons/fp.jpg"
	b.def.IconWatermark = "/common/icons/season.png"

	w := Weapon(1690783811, &b.def, lk, nil)

	if w.Hash != 1690783811 || w.Name != "The Forward Path" {
		t.Errorf("identity fields wrong: %+v", w)
	}
	if w.WeaponType != "Auto Rifle" {
		t.Errorf("WeaponType = %q", w.WeaponType)
	}
	if w.Slot != "Kinetic" {
		t.Errorf("Slot = %q", w.Slot)
	}
	if w.Frame == nil || *w.Frame != "Adaptive Frame" {
		t.Errorf("Frame = %v", w.Frame)
	}
	if w.RPM == nil || *w.RPM != 600 {
		t.Errorf("RPM = %v", w.RPM)
	}
	if w.Element == nil || *w.Element != "Kinetic" {
		t.Errorf("Element = %v", w.Element)
	}
	if w.Tier == nil || *w.Tier != "Legendary" {
		t.Errorf("Tier = %v", w.Tier)
	}
	if w.Source == nil || *w.Source != "Source: The Vault of Tests." {
		t.Errorf("Source = %v", w.Source)
	}
	if !w.Obtainable {
		t.Error("Obtainable = false, want true (live collectible)")
	}
	if w.Craftable || w.Enhanceable {
		t.Errorf("Craftable/Enhanceable = %v/%v, want false/false", w.Craftable, w.Enhanceable)
	}
	if w.Icon == nil || *w.Icon != "/common/icons/fp.jpg" {
		t.Errorf("Icon = %v", w.Icon)
	}
	if w.Watermark == nil || *w.Watermark != "/common/icons/season.png" {
		t.Errorf("Watermark = %v", w.Watermark)
	}

	// A weapon without icon fields keeps them nil.
	bare := Weapon(2, &newWeaponDef("Bare").def, lk, nil)
	if bare.Icon != nil || bare.Watermark != nil {
		t.Errorf("bare icon/watermark = %v/%v, want nil/nil", bare.Icon, bare.Watermark)
	}
}

func TestWeaponObtainableRule(t *testing.T) {
	lk := baseLookups()
	tests := []struct {
		name        string
		collectible uint32
		recipe      uint32
		want        bool
	}{
		{"live collectible", 900, 0, true},
		{"no collectible", 0, 0, false},
		{"unknown collectible hash", 999, 0, false},
		{"redacted collectible", 901, 0, false},
		{"blacklisted collectible", 902, 0, false},
		{"craftable only", 0, 777, true},
		{"craftable overrides dead collectible", 901, 777, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newWeaponDef("W")
			b.def.CollectibleHash = tt.collectible
			b.def.Inventory.RecipeItemHash = tt.recipe
			w := Weapon(1, &b.def, lk, nil)
			if w.Obtainable != tt.want {
				t.Errorf("Obtainable = %v, want %v", w.Obtainable, tt.want)
			}
			if w.Craftable != (tt.recipe != 0) {
				t.Errorf("Craftable = %v", w.Craftable)
			}
		})
	}
}

func TestWeaponSpeedStatBySubType(t *testing.T) {
	tests := []struct {
		name     string
		subType  int
		statHash uint32
	}{
		{"auto rifle uses RPM", 6, bungie.StatRoundsPerMinute},
		{"fusion uses charge time", 11, bungie.StatChargeTime},
		{"linear fusion uses charge time", 22, bungie.StatChargeTime},
		{"bow uses draw time", 31, bungie.StatDrawTime},
		{"sword uses swing speed", 18, bungie.StatSwingSpeed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newWeaponDef("W").stat(tt.statHash, 123)
			b.def.ItemSubType = tt.subType
			w := Weapon(1, &b.def, baseLookups(), nil)
			if w.RPM == nil || *w.RPM != 123 {
				t.Errorf("RPM = %v, want 123 via stat %d", w.RPM, tt.statHash)
			}
		})
	}

	t.Run("missing stat leaves RPM nil", func(t *testing.T) {
		w := Weapon(1, &newWeaponDef("W").def, baseLookups(), nil)
		if w.RPM != nil {
			t.Errorf("RPM = %v, want nil", w.RPM)
		}
	})
}

func TestWeaponSlotAndElementFallbacks(t *testing.T) {
	b := newWeaponDef("W")
	b.def.Inventory.BucketTypeHash = 12345 // unknown bucket
	b.def.DefaultDamageTypeHash = 99999    // unknown damage type
	b.def.Inventory.TierTypeName = ""
	w := Weapon(1, &b.def, baseLookups(), nil)
	if w.Slot != "Unknown" {
		t.Errorf("Slot = %q, want Unknown", w.Slot)
	}
	if w.Element != nil {
		t.Errorf("Element = %v, want nil", w.Element)
	}
	if w.Tier != nil {
		t.Errorf("Tier = %v, want nil", w.Tier)
	}

	for bucket, want := range map[uint32]string{
		bungie.BucketEnergy: "Energy",
		bungie.BucketPower:  "Power",
	} {
		b := newWeaponDef("W")
		b.def.Inventory.BucketTypeHash = bucket
		if got := Weapon(1, &b.def, baseLookups(), nil).Slot; got != want {
			t.Errorf("Slot for bucket %d = %q, want %q", bucket, got, want)
		}
	}
}

func TestWeaponEnhanceableFromColumns(t *testing.T) {
	cols := []models.PerkColumn{
		{Index: 0, Kind: models.KindTrait, Perks: []models.Perk{{Hash: 30, Name: "Zen Moment"}}},
		{Index: 1, Kind: models.KindTrait, Perks: []models.Perk{{Hash: 33, Name: "Rampage", Enhanced: true}}},
	}
	w := Weapon(1, &newWeaponDef("W").def, baseLookups(), cols)
	if !w.Enhanceable {
		t.Error("Enhanceable = false, want true (enhanced perk in pool)")
	}
}

// buildRandomRollWeapon wires a weapon with barrel, two trait columns, and
// an origin column, mirroring the live The Forward Path layout.
func buildRandomRollWeapon(lk *Lookups) *defBuilder {
	lk.PlugSets[100] = plugSet([2]uint32{20, 1}, [2]uint32{21, 1}, [2]uint32{21, 1}) // barrels, one duplicate
	lk.PlugSets[101] = plugSet([2]uint32{30, 1}, [2]uint32{32, 1})                   // trait col 1: base + enhanced
	lk.PlugSets[102] = plugSet([2]uint32{31, 1}, [2]uint32{33, 0})                   // trait col 2: enhanced can't roll
	lk.PlugSets[103] = plugSet([2]uint32{40, 1}, [2]uint32{41, 1})                   // origin traits
	return newWeaponDef("Test Rifle").
		socket(bungie.SocketCategoryIntrinsic, bungie.SocketEntry{SingleInitialItemHash: 10}).
		socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 100}).
		socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 101}).
		socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 102}).
		socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{ReusablePlugSetHash: 103})
}

func TestPerkColumns(t *testing.T) {
	lk := baseLookups()
	cols := PerkColumns(&buildRandomRollWeapon(lk).def, lk)

	if len(cols) != 4 {
		t.Fatalf("got %d columns, want 4", len(cols))
	}

	// Column 0: barrels, deduplicated, KindOther.
	if cols[0].Index != 0 || cols[0].Kind != models.KindOther || len(cols[0].Perks) != 2 {
		t.Errorf("col0 = %+v", cols[0])
	}
	// Column 1: base + enhanced trait, sorted by hash.
	if cols[1].Kind != models.KindTrait || len(cols[1].Perks) != 2 {
		t.Errorf("col1 = %+v", cols[1])
	}
	if !cols[1].Perks[1].Enhanced {
		t.Error("col1 enhanced perk not flagged")
	}
	// Perk 30 (Zen Moment) carries its icon; perk 32 has none in lookups.
	if cols[1].Perks[0].Icon == nil || *cols[1].Perks[0].Icon != "/common/icons/zen.jpg" {
		t.Errorf("perk icon = %v", cols[1].Perks[0].Icon)
	}
	if cols[1].Perks[1].Icon != nil {
		t.Errorf("iconless perk icon = %v, want nil", cols[1].Perks[1].Icon)
	}
	// Column 2: only the rollable base trait survives (33 can't roll).
	if len(cols[2].Perks) != 1 || cols[2].Perks[0].Hash != 31 {
		t.Errorf("col2 = %+v", cols[2])
	}
	// Column 3: origin kind.
	if cols[3].Kind != models.KindOrigin || len(cols[3].Perks) != 2 {
		t.Errorf("col3 = %+v", cols[3])
	}
}

func TestPerkColumnsEdgeCases(t *testing.T) {
	lk := baseLookups()

	t.Run("no weapon perks category", func(t *testing.T) {
		def := newWeaponDef("W").def
		if cols := PerkColumns(&def, lk); cols != nil {
			t.Errorf("cols = %+v, want nil", cols)
		}
	})

	t.Run("socket without plug set skipped, indexes stay ordinal", func(t *testing.T) {
		lk.PlugSets[101] = plugSet([2]uint32{30, 1})
		b := newWeaponDef("W").
			socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{}).                           // no plug set
			socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 999}). // unknown plug set
			socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 101})
		cols := PerkColumns(&b.def, lk)
		if len(cols) != 1 || cols[0].Index != 2 {
			t.Fatalf("cols = %+v, want single column with Index 2", cols)
		}
	})

	t.Run("out of range socket index", func(t *testing.T) {
		def := newWeaponDef("W").def
		def.Sockets.SocketCategories = append(def.Sockets.SocketCategories, struct {
			SocketCategoryHash uint32 `json:"socketCategoryHash"`
			SocketIndexes      []int  `json:"socketIndexes"`
		}{bungie.SocketCategoryWeaponPerks, []int{5, -1}})
		if cols := PerkColumns(&def, lk); cols != nil {
			t.Errorf("cols = %+v, want nil", cols)
		}
	})

	t.Run("unnamed plugs are skipped", func(t *testing.T) {
		lk.PlugSets[104] = plugSet([2]uint32{50, 1})
		b := newWeaponDef("W").
			socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 104})
		if cols := PerkColumns(&b.def, lk); cols != nil {
			t.Errorf("cols = %+v, want nil (only plug is unnamed)", cols)
		}
	})
}

func TestRollColumns(t *testing.T) {
	lk := baseLookups()
	cols := PerkColumns(&buildRandomRollWeapon(lk).def, lk)
	rollCols := RollColumns(cols)

	// Barrel column dropped; trait1 (enhanced preferred), trait2, origin kept.
	if len(rollCols) != 3 {
		t.Fatalf("got %d roll columns, want 3: %+v", len(rollCols), rollCols)
	}
	// Trait column 1 had base(30) + enhanced(32): enhanced-only survives.
	if len(rollCols[0].Perks) != 1 || rollCols[0].Perks[0].Hash != 32 {
		t.Errorf("trait col 1 = %+v, want only enhanced perk 32", rollCols[0])
	}
	// Trait column 2 had no enhanced: base perk stays.
	if len(rollCols[1].Perks) != 1 || rollCols[1].Perks[0].Hash != 31 {
		t.Errorf("trait col 2 = %+v, want base perk 31", rollCols[1])
	}
	// Origin column untouched.
	if rollCols[2].Kind != models.KindOrigin || len(rollCols[2].Perks) != 2 {
		t.Errorf("origin col = %+v", rollCols[2])
	}
}

func TestRollColumnsNoTraitsMeansNoRolls(t *testing.T) {
	cols := []models.PerkColumn{
		{Index: 0, Kind: models.KindOther, Perks: []models.Perk{{Hash: 20}}},
		{Index: 1, Kind: models.KindOrigin, Perks: []models.Perk{{Hash: 40}}},
	}
	if got := RollColumns(cols); got != nil {
		t.Errorf("RollColumns = %+v, want nil (origin alone is not a roll)", got)
	}
}
