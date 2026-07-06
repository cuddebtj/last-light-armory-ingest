package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// Read-side queries for the static-export pipeline. All results come back
// in deterministic (hash-ordered) sequence so exported artifacts are
// byte-stable across runs against identical data.

// PerkRow is a perk with its curated scores (owned by last-light-armory;
// exported read-only, NULL until that repo writes them).
type PerkRow struct {
	Hash     int64
	Name     string
	Enhanced bool
	PvEScore *int16
	PvPScore *int16
}

// WeaponPerkRow is one weapon→perk pool link, resolved to Bungie hashes.
type WeaponPerkRow struct {
	WeaponHash  int64
	ColumnIndex int16
	PerkHash    int64
}

// RollPerkRow is one perk of one roll, flattened for streaming; consecutive
// rows with the same RollID belong to the same roll.
type RollPerkRow struct {
	WeaponHash   int64
	RollID       int64
	ComboKey     string
	PvEScore     *float64
	PvPScore     *float64
	OverallScore *float64
	ColumnIndex  int16
	PerkHash     int64
}

// queryAll runs a query and scans every row with scan. A free function
// because Go methods cannot have type parameters.
func queryAll[T any](ctx context.Context, q Querier, sql string, scan func(pgx.Rows) (T, error)) ([]T, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("db: querying: %w", err)
	}
	defer rows.Close()

	var out []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("db: scanning row: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterating rows: %w", err)
	}
	return out, nil
}

// AllWeapons returns every weapon ordered by hash.
func (s *Store) AllWeapons(ctx context.Context) ([]models.Weapon, error) {
	return queryAll(ctx, s.pool, `
		SELECT hash, name, weapon_type, frame, rpm, slot, element, tier, source,
		       craftable, enhanceable, obtainable
		FROM weapon ORDER BY hash`,
		func(rows pgx.Rows) (models.Weapon, error) {
			var w models.Weapon
			err := rows.Scan(&w.Hash, &w.Name, &w.WeaponType, &w.Frame, &w.RPM, &w.Slot,
				&w.Element, &w.Tier, &w.Source, &w.Craftable, &w.Enhanceable, &w.Obtainable)
			return w, err
		})
}

// AllPerks returns every perk (with curated scores, read-only) ordered by hash.
func (s *Store) AllPerks(ctx context.Context) ([]PerkRow, error) {
	return queryAll(ctx, s.pool, `
		SELECT hash, name, enhanced, pve_score, pvp_score
		FROM perk ORDER BY hash`,
		func(rows pgx.Rows) (PerkRow, error) {
			var p PerkRow
			err := rows.Scan(&p.Hash, &p.Name, &p.Enhanced, &p.PvEScore, &p.PvPScore)
			return p, err
		})
}

// AllWeaponPerks returns the full perk-pool links ordered by weapon hash,
// column, then perk hash.
func (s *Store) AllWeaponPerks(ctx context.Context) ([]WeaponPerkRow, error) {
	return queryAll(ctx, s.pool, `
		SELECT w.hash, wp.column_index, p.hash
		FROM weapon_perk wp
		JOIN weapon w ON w.id = wp.weapon_id
		JOIN perk   p ON p.id = wp.perk_id
		ORDER BY w.hash, wp.column_index, p.hash`,
		func(rows pgx.Rows) (WeaponPerkRow, error) {
			var r WeaponPerkRow
			err := rows.Scan(&r.WeaponHash, &r.ColumnIndex, &r.PerkHash)
			return r, err
		})
}

// AllRollPerks returns every roll's perks flattened, ordered by weapon
// hash, combo key, then column, so consumers can group consecutive rows.
func (s *Store) AllRollPerks(ctx context.Context) ([]RollPerkRow, error) {
	return queryAll(ctx, s.pool, `
		SELECT w.hash, r.id, r.combo_key, r.pve_score, r.pvp_score, r.overall_score,
		       rp.column_index, p.hash
		FROM roll r
		JOIN weapon    w  ON w.id = r.weapon_id
		JOIN roll_perk rp ON rp.roll_id = r.id
		JOIN perk      p  ON p.id = rp.perk_id
		ORDER BY w.hash, r.combo_key, rp.column_index`,
		func(rows pgx.Rows) (RollPerkRow, error) {
			var r RollPerkRow
			err := rows.Scan(&r.WeaponHash, &r.RollID, &r.ComboKey, &r.PvEScore,
				&r.PvPScore, &r.OverallScore, &r.ColumnIndex, &r.PerkHash)
			return r, err
		})
}
