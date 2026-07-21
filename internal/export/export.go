// Package export builds the static JSON artifacts the website consumes.
//
// The database lives on a private network and is never exposed publicly
// (decision 2026-07-06); these artifacts are the only data that leaves it.
// The layout is optimized for a statically generated site (Next.js):
//
//	meta.json           manifest version, generation time, row counts
//	perks.json          every perk: hash, name, enhanced, curated scores
//	weapons/index.json  one slim entry per weapon for list/filter/search pages
//	weapons/<hash>.json full detail: fields, perk pool columns, rolls
//
// Weapon documents reference perks by hash only; the site joins names from
// perks.json. Output is deterministic: identical database contents produce
// byte-identical artifacts, so re-exports diff cleanly in git.
package export

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// Meta describes one export run.
type Meta struct {
	ManifestVersion string    `json:"manifest_version"`
	GeneratedAt     time.Time `json:"generated_at"`
	WeaponCount     int       `json:"weapon_count"`
	PerkCount       int       `json:"perk_count"`
	RollCount       int       `json:"roll_count"`
}

// Perk is one entry in perks.json. Icon is a CDN path relative to
// https://www.bungie.net.
type Perk struct {
	Hash     int64   `json:"hash"`
	Name     string  `json:"name"`
	Enhanced bool    `json:"enhanced"`
	Icon     *string `json:"icon"`
	PvEScore *int16  `json:"pve_score"`
	PvPScore *int16  `json:"pvp_score"`
}

// WeaponSummary is one entry in weapons/index.json: everything a list,
// filter, or perk-combo search needs without fetching the full document.
// Columns carries the full per-weapon perk pool (column -> perk hashes,
// joined client-side against perks.json names) so perk filtering works
// against the index alone — added 2026-07-21 to close the data gap
// last-light-armory's CLAUDE.md flagged ("per-weapon perk pools aren't in
// index.json"); previously only present on the per-weapon detail document.
type WeaponSummary struct {
	Hash        int64        `json:"hash"`
	Name        string       `json:"name"`
	Type        string       `json:"type"` // also the archetype
	Slot        string       `json:"slot"`
	Element     *string      `json:"element"`
	Tier        *string      `json:"tier"`
	Frame       *string      `json:"frame"`
	RPM         *int         `json:"rpm"`
	Icon        *string      `json:"icon"`         // CDN path relative to https://www.bungie.net
	Watermark   *string      `json:"watermark"`    // season badge, overlaid on icon
	AmmoType    *string      `json:"ammo_type"`    // Primary / Special / Heavy
	BreakerType *string      `json:"breaker_type"` // intrinsic champion-breaking capability, e.g. "Shield Piercing"
	Craftable   bool         `json:"craftable"`
	Enhanceable bool         `json:"enhanceable"`
	Obtainable  bool         `json:"obtainable"`
	RollCount   int          `json:"roll_count"`
	Columns     []PerkColumn `json:"columns"`

	// Ranking fields, owned and written by last-light-armory's scoring
	// job (this repo only reads weapon_ranking, never writes it). All nil
	// for a weapon with zero rolls, which never gets a ranking row (see
	// that repo's CLAUDE.md, "best single roll represents the weapon").
	// Weapon-level rank, not combo-level: "v1 may launch on weapon-level
	// rank; combo-level rank is the design goal" per that repo's own
	// filtering-direction notes — combo-level needs scoring_config's
	// weights/base_blend exported too, which this does not yet do.
	OverallScore    *float64 `json:"overall_score"`
	PvEScore        *float64 `json:"pve_score"`
	PvPScore        *float64 `json:"pvp_score"`
	PopularityScore *float64 `json:"popularity_score"` // Phase 6 (community voting), not started — always null for now
}

// PerkColumn is one column of a weapon's full perk pool.
type PerkColumn struct {
	Index int16   `json:"index"`
	Perks []int64 `json:"perks"` // perk hashes, sorted
}

// RollSlot is one perk within a roll.
type RollSlot struct {
	Column int16 `json:"column"`
	Hash   int64 `json:"hash"`
}

// Roll is one legal perk combination with its curated scores (NULL until
// last-light-armory writes them).
type Roll struct {
	Key     string     `json:"key"`
	Perks   []RollSlot `json:"perks"`
	PvE     *float64   `json:"pve_score"`
	PvP     *float64   `json:"pvp_score"`
	Overall *float64   `json:"overall_score"`
}

// WeaponDoc is one weapons/<hash>.json document. Columns lives on the
// embedded WeaponSummary (also exported in index.json, see its doc comment).
type WeaponDoc struct {
	WeaponSummary
	Source *string `json:"source"`
	Rolls  []Roll  `json:"rolls"`
}

// Site is a fully assembled export, ready to write.
type Site struct {
	Meta  Meta
	Perks []Perk
	Index []WeaponSummary
	Docs  []*WeaponDoc // sorted by hash, mirroring Index
}

// Build assembles a Site from database rows. Inputs are expected in the
// hash-ordered sequences the db package produces; rolls are re-sorted by
// combo key so output never depends on database row identity.
//
// A link or roll referencing a weapon absent from weapons is a data
// integrity failure and returns an error rather than silently dropping
// data. rankings is the one exception: a weapon-less-common absence there
// (zero-roll weapons) is expected, not an error — see WeaponSummary's
// ranking field doc comment.
func Build(version string, now time.Time, weapons []models.Weapon, perks []db.PerkRow, links []db.WeaponPerkRow, rollRows []db.RollPerkRow, rankings []db.WeaponRankingRow) (*Site, error) {
	docs := make(map[int64]*WeaponDoc, len(weapons))
	site := &Site{
		Meta: Meta{ManifestVersion: version, GeneratedAt: now.UTC(), WeaponCount: len(weapons), PerkCount: len(perks)},
	}

	for _, p := range perks {
		site.Perks = append(site.Perks, Perk{Hash: p.Hash, Name: p.Name, Enhanced: p.Enhanced, Icon: p.Icon, PvEScore: p.PvEScore, PvPScore: p.PvPScore})
	}

	for _, w := range weapons {
		doc := &WeaponDoc{
			WeaponSummary: WeaponSummary{
				Hash: w.Hash, Name: w.Name, Type: w.WeaponType, Slot: w.Slot,
				Element: w.Element, Tier: w.Tier, Frame: w.Frame, RPM: w.RPM,
				Icon: w.Icon, Watermark: w.Watermark,
				AmmoType: w.AmmoType, BreakerType: w.BreakerType,
				Craftable: w.Craftable, Enhanceable: w.Enhanceable, Obtainable: w.Obtainable,
				Columns: []PerkColumn{},
			},
			Source: w.Source,
			Rolls:  []Roll{},
		}
		docs[w.Hash] = doc
		site.Docs = append(site.Docs, doc)
	}

	// Group pool links into columns; rows arrive ordered by weapon, column,
	// perk, so appending preserves sorted output.
	for _, l := range links {
		doc, ok := docs[l.WeaponHash]
		if !ok {
			return nil, fmt.Errorf("export: weapon_perk references unknown weapon hash %d", l.WeaponHash)
		}
		cols := doc.Columns
		if n := len(cols); n == 0 || cols[n-1].Index != l.ColumnIndex {
			doc.Columns = append(cols, PerkColumn{Index: l.ColumnIndex})
		}
		last := &doc.Columns[len(doc.Columns)-1]
		last.Perks = append(last.Perks, l.PerkHash)
	}

	// Apply rankings. Unlike links/rolls, a missing weapon_ranking row for
	// a known weapon is expected (zero-roll weapons); an unknown weapon
	// hash is still a data integrity failure, same as the other joins.
	for _, r := range rankings {
		doc, ok := docs[r.WeaponHash]
		if !ok {
			return nil, fmt.Errorf("export: weapon_ranking references unknown weapon hash %d", r.WeaponHash)
		}
		doc.OverallScore = r.OverallScore
		doc.PvEScore = r.PvEScore
		doc.PvPScore = r.PvPScore
		doc.PopularityScore = r.PopularityScore
	}

	// Group flattened roll rows into rolls; rows arrive ordered by weapon,
	// combo key, column, so a RollID change marks a roll boundary.
	var lastRollID int64 = -1
	var current *Roll
	for _, r := range rollRows {
		doc, ok := docs[r.WeaponHash]
		if !ok {
			return nil, fmt.Errorf("export: roll references unknown weapon hash %d", r.WeaponHash)
		}
		if r.RollID != lastRollID {
			doc.Rolls = append(doc.Rolls, Roll{Key: r.ComboKey, PvE: r.PvEScore, PvP: r.PvPScore, Overall: r.OverallScore})
			current = &doc.Rolls[len(doc.Rolls)-1]
			lastRollID = r.RollID
			site.Meta.RollCount++
		}
		current.Perks = append(current.Perks, RollSlot{Column: r.ColumnIndex, Hash: r.PerkHash})
	}

	// Determinism: roll order within a weapon follows combo key (already
	// sorted by the query, but enforced here so Build's contract does not
	// depend on the caller); the index mirrors doc order.
	for _, doc := range site.Docs {
		sort.Slice(doc.Rolls, func(i, j int) bool { return doc.Rolls[i].Key < doc.Rolls[j].Key })
		doc.RollCount = len(doc.Rolls)
		site.Index = append(site.Index, doc.WeaponSummary)
	}
	return site, nil
}

// Write materializes the site under dir:
//
//	dir/meta.json
//	dir/perks.json
//	dir/weapons/index.json
//	dir/weapons/<hash>.json
func (s *Site) Write(dir string) error {
	weaponsDir := filepath.Join(dir, "weapons")
	if err := os.MkdirAll(weaponsDir, 0o755); err != nil {
		return fmt.Errorf("export: creating %s: %w", weaponsDir, err)
	}

	if err := writeJSON(filepath.Join(dir, "meta.json"), s.Meta); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "perks.json"), s.Perks); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(weaponsDir, "index.json"), s.Index); err != nil {
		return err
	}
	for _, doc := range s.Docs {
		if err := writeJSON(filepath.Join(weaponsDir, fmt.Sprintf("%d.json", doc.Hash)), doc); err != nil {
			return err
		}
	}
	return nil
}

// writeJSON writes v as indented JSON (git-diff friendly; CDNs compress on
// the wire anyway).
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("export: marshaling %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("export: writing %s: %w", path, err)
	}
	return nil
}
