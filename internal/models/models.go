// Package models holds the plain domain structs shared between the
// categorization, roll-generation, and database layers.
//
// Bungie hashes are uint32 on the wire but stored as int64 here to match
// Postgres BIGINT columns without conversion at every query site.
package models

// Weapon is one weapon inventory item, keyed by Bungie hash.
// WeaponType doubles as the archetype (decision 2026-07-06).
type Weapon struct {
	Hash        int64
	Name        string
	WeaponType  string  // e.g. "Auto Rifle"; also the archetype
	Frame       *string // intrinsic perk name, e.g. "Adaptive Frame"
	RPM         *int    // fire-rate stat; draw/charge time (ms) or swing speed for bows/fusions/swords
	Slot        string  // Kinetic / Energy / Power / Unknown
	Element     *string // damage type name
	Tier        *string // e.g. "Legendary", "Exotic"
	Source      *string // collectible sourceString when present
	Icon        *string // CDN path relative to https://www.bungie.net
	Watermark   *string // season/expansion badge CDN path, overlaid on Icon
	Craftable   bool
	Enhanceable bool
	Obtainable  bool
}

// Perk is one perk plug item, keyed by Bungie hash.
type Perk struct {
	Hash     int64
	Name     string
	Enhanced bool    // enhanced-trait variant of a base perk
	Icon     *string // CDN path relative to https://www.bungie.net
}

// PerkColumn is one perk column on a weapon: the set of perks that can roll
// in that socket position.
type PerkColumn struct {
	// Index is the 0-based ordinal of this column within the weapon's
	// WEAPON PERKS sockets, stable across manifest updates.
	Index int
	Kind  ColumnKind
	Perks []Perk
}

// ColumnKind classifies a perk column for roll generation.
type ColumnKind int

const (
	// KindOther covers barrels, magazines, batteries, and similar columns:
	// recorded in weapon_perk but excluded from roll skeletons.
	KindOther ColumnKind = iota
	// KindTrait is a main trait column (3rd/4th column perks).
	KindTrait
	// KindOrigin is an origin-trait column.
	KindOrigin
)

// RollPerk is one perk within a roll, tagged with its column index.
type RollPerk struct {
	Column   int
	PerkHash int64
}
