package db

import "github.com/cuddebtj/last-light-armory-ingest/internal/models"

// Array builders convert model slices into the parallel arrays fed to
// Postgres unnest(). Kept as pure functions so they can be unit-tested
// without a database.

// perkArrays flattens perks into parallel unnest arrays.
func perkArrays(perks []models.Perk) (hashes []int64, names []string, enhanced []bool) {
	hashes = make([]int64, len(perks))
	names = make([]string, len(perks))
	enhanced = make([]bool, len(perks))
	for i, p := range perks {
		hashes[i] = p.Hash
		names[i] = p.Name
		enhanced[i] = p.Enhanced
	}
	return hashes, names, enhanced
}

// weaponArrayArgs carries the parallel arrays for the weapon upsert.
// Nullable columns use pointer slices: pgx encodes a nil element as SQL NULL.
type weaponArrayArgs struct {
	hashes      []int64
	names       []string
	types       []string
	frames      []*string
	rpms        []*int32
	slots       []string
	elements    []*string
	tiers       []*string
	sources     []*string
	craftable   []bool
	enhanceable []bool
	obtainable  []bool
}

// weaponArrays flattens weapons into parallel unnest arrays.
func weaponArrays(weapons []models.Weapon) weaponArrayArgs {
	n := len(weapons)
	a := weaponArrayArgs{
		hashes:      make([]int64, n),
		names:       make([]string, n),
		types:       make([]string, n),
		frames:      make([]*string, n),
		rpms:        make([]*int32, n),
		slots:       make([]string, n),
		elements:    make([]*string, n),
		tiers:       make([]*string, n),
		sources:     make([]*string, n),
		craftable:   make([]bool, n),
		enhanceable: make([]bool, n),
		obtainable:  make([]bool, n),
	}
	for i, w := range weapons {
		a.hashes[i] = w.Hash
		a.names[i] = w.Name
		a.types[i] = w.WeaponType
		a.frames[i] = w.Frame
		if w.RPM != nil {
			v := int32(*w.RPM)
			a.rpms[i] = &v
		}
		a.slots[i] = w.Slot
		a.elements[i] = w.Element
		a.tiers[i] = w.Tier
		a.sources[i] = w.Source
		a.craftable[i] = w.Craftable
		a.enhanceable[i] = w.Enhanceable
		a.obtainable[i] = w.Obtainable
	}
	return a
}

// columnArrays flattens perk columns into parallel (perk_hash, column_index)
// arrays for the weapon_perk reconciliation query.
func columnArrays(columns []models.PerkColumn) (perkHashes []int64, columnIndexes []int16) {
	for _, col := range columns {
		for _, p := range col.Perks {
			perkHashes = append(perkHashes, p.Hash)
			columnIndexes = append(columnIndexes, int16(col.Index))
		}
	}
	return perkHashes, columnIndexes
}
