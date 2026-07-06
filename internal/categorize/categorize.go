// Package categorize derives the weapon fields this project stores from raw
// Bungie definitions: weapon type, frame, slot, element, fire-rate stat,
// craftable/enhanceable/obtainable flags, and the per-column perk pools.
//
// Everything here is a pure function over definitions + lookup tables, which
// keeps the rules unit-testable without any network or database access.
//
// Decisions encoded here (confirmed 2026-07-06):
//   - Archetype IS the weapon type ("Auto Rifle", "Sniper Rifle", ...), so
//     no separate archetype field is derived.
//   - Obtainable (heuristic v1): the weapon has a live collectible entry
//     (present, not redacted, not blacklisted) OR is craftable. Refine with
//     an UPDATE later if validation against known-vaulted weapons demands it.
package categorize

import (
	"sort"
	"strconv"

	"github.com/cuddebtj/last-light-armory-ingest/internal/bungie"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// Plug itemTypeDisplayName values used to classify perk columns (verified
// against the live manifest 2026-07-06).
const (
	plugTypeTrait         = "Trait"
	plugTypeEnhancedTrait = "Enhanced Trait"
	plugTypeOriginTrait   = "Origin Trait"
)

// PlugInfo is the minimal information retained for every plug item (perks,
// intrinsics) while streaming the 200 MB item table: just enough to name a
// perk and classify its column.
type PlugInfo struct {
	Name            string
	TypeDisplayName string
}

// Lookups bundles the cross-definition tables needed to categorize one
// weapon. All maps are read-only after construction and therefore safe to
// share across worker goroutines.
type Lookups struct {
	// DamageTypes maps DestinyDamageTypeDefinition hash -> display name.
	DamageTypes map[uint32]string
	// Collectibles maps DestinyCollectibleDefinition hash -> definition.
	Collectibles map[uint32]bungie.CollectibleDefinition
	// PlugSets maps DestinyPlugSetDefinition hash -> definition.
	PlugSets map[uint32]bungie.PlugSetDefinition
	// Plugs maps inventory item hash -> minimal plug info, for every item
	// (weapons included; harmless).
	Plugs map[uint32]PlugInfo
}

// IsWeapon reports whether an inventory item definition is a weapon worth
// importing. In the current manifest itemType==3 has zero redacted,
// blacklisted, or dummy entries, but the guards are kept in case a future
// manifest changes that.
func IsWeapon(def *bungie.InventoryItemDefinition) bool {
	return def.ItemType == bungie.ItemTypeWeapon &&
		!def.Redacted &&
		!def.Blacklisted &&
		def.DisplayProperties.Name != ""
}

// Weapon derives the stored weapon row from a definition and lookups.
// perkColumns must be the result of PerkColumns for the same definition; it
// feeds the enhanceable flag.
func Weapon(hash uint32, def *bungie.InventoryItemDefinition, lk *Lookups, perkColumns []models.PerkColumn) models.Weapon {
	w := models.Weapon{
		Hash:       int64(hash),
		Name:       def.DisplayProperties.Name,
		WeaponType: def.ItemTypeDisplayName,
		Slot:       slotName(def.Inventory.BucketTypeHash),
		Craftable:  def.Inventory.RecipeItemHash != 0,
	}

	if tier := def.Inventory.TierTypeName; tier != "" {
		w.Tier = &tier
	}
	if name, ok := lk.DamageTypes[def.DefaultDamageTypeHash]; ok && name != "" {
		w.Element = &name
	}
	if frame := intrinsicName(def, lk); frame != "" {
		w.Frame = &frame
	}
	if rpm, ok := speedStat(def); ok {
		w.RPM = &rpm
	}

	// Obtainable heuristic v1 + source string, both from the collectible.
	if def.CollectibleHash != 0 {
		if col, ok := lk.Collectibles[def.CollectibleHash]; ok && !col.Redacted && !col.Blacklisted {
			w.Obtainable = true
			if col.SourceString != "" {
				src := col.SourceString
				w.Source = &src
			}
		}
	}
	if w.Craftable {
		w.Obtainable = true
	}

	for _, col := range perkColumns {
		for _, p := range col.Perks {
			if p.Enhanced {
				w.Enhanceable = true
				break
			}
		}
	}
	return w
}

// PerkColumns extracts the rollable perk pool for each WEAPON PERKS socket
// on a weapon: every plug in the socket's plug set with currentlyCanRoll,
// resolved to a named perk. Columns with no rollable, resolvable plugs are
// omitted; Index still reflects the socket's ordinal position within the
// WEAPON PERKS category so indexes stay stable when a middle column is empty.
//
// Perks within a column are sorted by hash for deterministic output.
func PerkColumns(def *bungie.InventoryItemDefinition, lk *Lookups) []models.PerkColumn {
	var out []models.PerkColumn
	for ordinal, socketIdx := range weaponPerkSocketIndexes(def) {
		if socketIdx < 0 || socketIdx >= len(def.Sockets.SocketEntries) {
			continue
		}
		entry := def.Sockets.SocketEntries[socketIdx]
		plugSetHash := entry.RandomizedPlugSetHash
		if plugSetHash == 0 {
			plugSetHash = entry.ReusablePlugSetHash
		}
		if plugSetHash == 0 {
			continue
		}
		plugSet, ok := lk.PlugSets[plugSetHash]
		if !ok {
			continue
		}

		col := models.PerkColumn{Index: ordinal}
		seen := map[uint32]bool{}
		for _, item := range plugSet.ReusablePlugItems {
			if !item.CurrentlyCanRoll || seen[item.PlugItemHash] {
				continue
			}
			seen[item.PlugItemHash] = true
			info, ok := lk.Plugs[item.PlugItemHash]
			if !ok || info.Name == "" {
				continue
			}
			col.Perks = append(col.Perks, models.Perk{
				Hash:     int64(item.PlugItemHash),
				Name:     info.Name,
				Enhanced: info.TypeDisplayName == plugTypeEnhancedTrait,
			})
			switch info.TypeDisplayName {
			case plugTypeTrait, plugTypeEnhancedTrait:
				col.Kind = models.KindTrait
			case plugTypeOriginTrait:
				if col.Kind != models.KindTrait {
					col.Kind = models.KindOrigin
				}
			}
		}
		if len(col.Perks) == 0 {
			continue
		}
		sort.Slice(col.Perks, func(i, j int) bool { return col.Perks[i].Hash < col.Perks[j].Hash })
		out = append(out, col)
	}
	return out
}

// RollColumns selects and filters the columns that define a roll skeleton
// (decision 2026-07-06): trait columns and origin columns only — barrels,
// magazines, and other hardware never multiply combinations. Within a trait
// column, enhanced variants are preferred: if any enhanced perk exists the
// column keeps only enhanced perks, otherwise it keeps the base perks.
// Origin columns are kept as-is (no enhanced variants exist for them).
//
// The result is nil when the weapon has no trait columns: origin traits
// alone do not constitute a roll.
func RollColumns(perkColumns []models.PerkColumn) []models.PerkColumn {
	var out []models.PerkColumn
	hasTrait := false
	for _, col := range perkColumns {
		switch col.Kind {
		case models.KindTrait:
			hasTrait = true
			out = append(out, models.PerkColumn{Index: col.Index, Kind: col.Kind, Perks: preferEnhanced(col.Perks)})
		case models.KindOrigin:
			out = append(out, col)
		}
	}
	if !hasTrait {
		return nil
	}
	return out
}

// preferEnhanced returns only the enhanced perks when at least one exists,
// otherwise the input unchanged.
func preferEnhanced(perks []models.Perk) []models.Perk {
	var enhanced []models.Perk
	for _, p := range perks {
		if p.Enhanced {
			enhanced = append(enhanced, p)
		}
	}
	if len(enhanced) == 0 {
		return perks
	}
	return enhanced
}

// weaponPerkSocketIndexes returns the socket indexes grouped under the
// WEAPON PERKS category, in category order (which is column order).
func weaponPerkSocketIndexes(def *bungie.InventoryItemDefinition) []int {
	for _, cat := range def.Sockets.SocketCategories {
		if cat.SocketCategoryHash == bungie.SocketCategoryWeaponPerks {
			return cat.SocketIndexes
		}
	}
	return nil
}

// intrinsicName resolves the weapon's frame: the initial plug of the socket
// under the INTRINSIC TRAITS category.
func intrinsicName(def *bungie.InventoryItemDefinition, lk *Lookups) string {
	for _, cat := range def.Sockets.SocketCategories {
		if cat.SocketCategoryHash != bungie.SocketCategoryIntrinsic {
			continue
		}
		for _, idx := range cat.SocketIndexes {
			if idx < 0 || idx >= len(def.Sockets.SocketEntries) {
				continue
			}
			if h := def.Sockets.SocketEntries[idx].SingleInitialItemHash; h != 0 {
				if info, ok := lk.Plugs[h]; ok {
					return info.Name
				}
			}
		}
	}
	return ""
}

// speedStat picks the fire-rate-equivalent stat for a weapon: rounds per
// minute for most types; charge time for fusion and linear fusion rifles;
// draw time for bows; swing speed for swords (DestinyItemSubType values 11,
// 22, 31, 18 respectively, verified live).
func speedStat(def *bungie.InventoryItemDefinition) (int, bool) {
	statHash := bungie.StatRoundsPerMinute
	switch def.ItemSubType {
	case 11, 22: // Fusion Rifle, Linear Fusion Rifle
		statHash = bungie.StatChargeTime
	case 31: // Combat Bow
		statHash = bungie.StatDrawTime
	case 18: // Sword
		statHash = bungie.StatSwingSpeed
	}
	s, ok := def.Stats.Stats[strconv.FormatUint(uint64(statHash), 10)]
	if !ok {
		return 0, false
	}
	return s.Value, true
}

// slotName maps an inventory bucket hash to the human slot name.
func slotName(bucket uint32) string {
	switch bucket {
	case bungie.BucketKinetic:
		return "Kinetic"
	case bungie.BucketEnergy:
		return "Energy"
	case bungie.BucketPower:
		return "Power"
	default:
		return "Unknown"
	}
}
