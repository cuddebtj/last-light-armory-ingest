package categorize

import (
	"testing"

	"github.com/cuddebtj/last-light-armory-ingest/internal/bungie"
)

// TestIntrinsicNameGuards exercises the malformed-definition guards in
// frame resolution: out-of-range socket indexes, zeroed plug hashes, and
// plugs missing from the lookup table must all yield "no frame" instead of
// panicking.
func TestIntrinsicNameGuards(t *testing.T) {
	lk := baseLookups()

	t.Run("socket index out of range", func(t *testing.T) {
		def := newWeaponDef("W").def
		def.Sockets.SocketCategories = append(def.Sockets.SocketCategories, struct {
			SocketCategoryHash uint32 `json:"socketCategoryHash"`
			SocketIndexes      []int  `json:"socketIndexes"`
		}{bungie.SocketCategoryIntrinsic, []int{7, -2}})
		if w := Weapon(1, &def, lk, nil); w.Frame != nil {
			t.Errorf("Frame = %v, want nil", w.Frame)
		}
	})

	t.Run("zero plug hash", func(t *testing.T) {
		b := newWeaponDef("W").
			socket(bungie.SocketCategoryIntrinsic, bungie.SocketEntry{SingleInitialItemHash: 0})
		if w := Weapon(1, &b.def, lk, nil); w.Frame != nil {
			t.Errorf("Frame = %v, want nil", w.Frame)
		}
	})

	t.Run("non-intrinsic categories are skipped", func(t *testing.T) {
		// A weapon whose only socket category is WEAPON PERKS: frame
		// resolution must walk past it and come up empty.
		lk.PlugSets[101] = plugSet([2]uint32{30, 1})
		b := newWeaponDef("W").
			socket(bungie.SocketCategoryWeaponPerks, bungie.SocketEntry{RandomizedPlugSetHash: 101})
		if w := Weapon(1, &b.def, lk, nil); w.Frame != nil {
			t.Errorf("Frame = %v, want nil", w.Frame)
		}
	})

	t.Run("plug missing from lookups", func(t *testing.T) {
		b := newWeaponDef("W").
			socket(bungie.SocketCategoryIntrinsic, bungie.SocketEntry{SingleInitialItemHash: 424242})
		if w := Weapon(1, &b.def, lk, nil); w.Frame != nil {
			t.Errorf("Frame = %v, want nil", w.Frame)
		}
	})
}
