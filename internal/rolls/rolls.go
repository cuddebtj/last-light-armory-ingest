// Package rolls generates every legal perk combination ("roll skeleton")
// for a weapon from its roll-eligible perk columns.
//
// Generation is streaming: combinations are yielded one at a time through a
// callback instead of materialized into a slice, so memory stays constant
// regardless of how many combinations a weapon has. Order is deterministic
// (odometer over columns sorted by index, perks in the order provided),
// which — together with ComboKey — makes re-ingestion produce byte-identical
// keys and therefore clean idempotent upserts.
package rolls

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"

	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// Count returns the number of combinations Generate will yield for the
// given columns: the product of the column sizes, or 0 when there are no
// non-empty columns (empty columns are impossible by construction in
// categorize, but guarded anyway).
func Count(columns []models.PerkColumn) int64 {
	product := int64(0)
	for _, col := range columns {
		if len(col.Perks) == 0 {
			continue
		}
		if product == 0 {
			product = 1
		}
		product *= int64(len(col.Perks))
	}
	return product
}

// Generate yields every combination of one perk per column, in
// deterministic odometer order (last column varies fastest). The slice
// passed to yield is reused between calls — callers must copy it if they
// retain it past the callback's return.
//
// Returning an error from yield aborts generation and propagates the error.
func Generate(columns []models.PerkColumn, yield func(combo []models.RollPerk) error) error {
	// Work on a copy sorted by column index so output order never depends
	// on caller ordering.
	cols := make([]models.PerkColumn, 0, len(columns))
	for _, c := range columns {
		if len(c.Perks) > 0 {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Index < cols[j].Index })

	combo := make([]models.RollPerk, len(cols))
	odometer := make([]int, len(cols))
	for {
		for i, c := range cols {
			combo[i] = models.RollPerk{Column: c.Index, PerkHash: c.Perks[odometer[i]].Hash}
		}
		if err := yield(combo); err != nil {
			return err
		}

		// Advance the odometer from the rightmost column.
		pos := len(cols) - 1
		for pos >= 0 {
			odometer[pos]++
			if odometer[pos] < len(cols[pos].Perks) {
				break
			}
			odometer[pos] = 0
			pos--
		}
		if pos < 0 {
			return nil
		}
	}
}

// ComboKey returns the deterministic identity of a roll: the lowercase hex
// SHA-256 of its "column:perkHash" pairs sorted by column. Two rolls with
// the same perks in the same columns always produce the same key, so
// (weapon_id, combo_key) is a stable natural key across ingestion runs.
func ComboKey(combo []models.RollPerk) string {
	sorted := make([]models.RollPerk, len(combo))
	copy(sorted, combo)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Column < sorted[j].Column })

	h := sha256.New()
	for i, rp := range sorted {
		if i > 0 {
			h.Write([]byte{'|'})
		}
		h.Write([]byte(strconv.Itoa(rp.Column)))
		h.Write([]byte{':'})
		h.Write([]byte(strconv.FormatInt(rp.PerkHash, 10)))
	}
	return hex.EncodeToString(h.Sum(nil))
}
