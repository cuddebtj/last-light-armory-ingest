package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// Querier is the slice of pgxpool.Pool the Store needs. Abstracting it lets
// unit tests inject failures (via pgxmock) into paths a live database can't
// realistically produce: scan errors, mid-iteration failures, commit
// failures, and similar.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// Store implements the repositories over the shared database. All methods
// are safe for concurrent use.
type Store struct {
	pool Querier
}

// NewStore wraps a connection pool (or any Querier) in a Store.
func NewStore(pool Querier) *Store { return &Store{pool: pool} }

// Counts summarizes the effect of an upsert batch.
type Counts struct {
	Inserted  int
	Updated   int
	Unchanged int
}

// Roll is one generated roll skeleton ready for persistence.
type Roll struct {
	ComboKey string
	Perks    []models.RollPerk
}

// GetSyncState returns the last recorded manifest version, with found=false
// when no ingest has ever completed.
func (s *Store) GetSyncState(ctx context.Context) (version string, found bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT last_manifest_version FROM manifest_sync_state WHERE id = 1`,
	).Scan(&version)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("db: reading sync state: %w", err)
	}
	return version, true, nil
}

// UpsertSyncState records a manifest check. changed=true also bumps
// last_changed_at, marking a completed import of a new version.
func (s *Store) UpsertSyncState(ctx context.Context, version string, changed bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO manifest_sync_state (id, last_manifest_version, last_checked_at, last_changed_at)
		VALUES (1, $1, now(), CASE WHEN $2 THEN now() ELSE NULL END)
		ON CONFLICT (id) DO UPDATE SET
			last_manifest_version = excluded.last_manifest_version,
			last_checked_at       = now(),
			last_changed_at       = CASE WHEN $2 THEN now() ELSE manifest_sync_state.last_changed_at END`,
		version, changed)
	if err != nil {
		return fmt.Errorf("db: upserting sync state: %w", err)
	}
	return nil
}

// UpsertPerks idempotently upserts perk identity rows keyed on Bungie hash.
// The curated pve_score/pvp_score columns are never touched — they belong
// to last-light-armory.
func (s *Store) UpsertPerks(ctx context.Context, perks []models.Perk) (Counts, error) {
	if len(perks) == 0 {
		return Counts{}, nil
	}
	hashes, names, enhanced := perkArrays(perks)

	rows, err := s.pool.Query(ctx, `
		INSERT INTO perk (hash, name, enhanced)
		SELECT * FROM unnest($1::bigint[], $2::text[], $3::boolean[])
		ON CONFLICT (hash) DO UPDATE SET
			name = excluded.name,
			enhanced = excluded.enhanced,
			updated_at = now()
		WHERE (perk.name, perk.enhanced) IS DISTINCT FROM (excluded.name, excluded.enhanced)
		RETURNING (xmax = 0) AS inserted`,
		hashes, names, enhanced)
	if err != nil {
		return Counts{}, fmt.Errorf("db: upserting perks: %w", err)
	}
	return tallyUpsert(rows, len(perks))
}

// UpsertWeapons idempotently upserts weapon rows keyed on Bungie hash.
func (s *Store) UpsertWeapons(ctx context.Context, weapons []models.Weapon) (Counts, error) {
	if len(weapons) == 0 {
		return Counts{}, nil
	}
	a := weaponArrays(weapons)

	rows, err := s.pool.Query(ctx, `
		INSERT INTO weapon (hash, name, weapon_type, frame, rpm, slot, element, tier, source,
		                    craftable, enhanceable, obtainable)
		SELECT * FROM unnest($1::bigint[], $2::text[], $3::text[], $4::text[], $5::int[], $6::text[],
		                     $7::text[], $8::text[], $9::text[], $10::boolean[], $11::boolean[], $12::boolean[])
		ON CONFLICT (hash) DO UPDATE SET
			name = excluded.name, weapon_type = excluded.weapon_type, frame = excluded.frame,
			rpm = excluded.rpm, slot = excluded.slot, element = excluded.element,
			tier = excluded.tier, source = excluded.source, craftable = excluded.craftable,
			enhanceable = excluded.enhanceable, obtainable = excluded.obtainable,
			updated_at = now()
		WHERE (weapon.name, weapon.weapon_type, weapon.frame, weapon.rpm, weapon.slot, weapon.element,
		       weapon.tier, weapon.source, weapon.craftable, weapon.enhanceable, weapon.obtainable)
		  IS DISTINCT FROM
		      (excluded.name, excluded.weapon_type, excluded.frame, excluded.rpm, excluded.slot, excluded.element,
		       excluded.tier, excluded.source, excluded.craftable, excluded.enhanceable, excluded.obtainable)
		RETURNING (xmax = 0) AS inserted`,
		a.hashes, a.names, a.types, a.frames, a.rpms, a.slots,
		a.elements, a.tiers, a.sources, a.craftable, a.enhanceable, a.obtainable)
	if err != nil {
		return Counts{}, fmt.Errorf("db: upserting weapons: %w", err)
	}
	return tallyUpsert(rows, len(weapons))
}

// PerkIDsByHash returns the perk primary keys for the given hashes, for
// resolving roll_perk foreign keys without per-row lookups.
func (s *Store) PerkIDsByHash(ctx context.Context) (map[int64]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT hash, id FROM perk`)
	if err != nil {
		return nil, fmt.Errorf("db: loading perk ids: %w", err)
	}
	defer rows.Close()

	out := map[int64]int64{}
	for rows.Next() {
		var hash, id int64
		if err := rows.Scan(&hash, &id); err != nil {
			return nil, fmt.Errorf("db: scanning perk id: %w", err)
		}
		out[hash] = id
	}
	return out, rows.Err()
}

// ReplaceWeaponPerks reconciles the weapon_perk links for one weapon:
// missing links are inserted, links no longer present in the manifest are
// deleted (scoped to this weapon only — never a global wipe), matching
// links are untouched. Perks are referenced by Bungie hash and resolved
// in-database.
func (s *Store) ReplaceWeaponPerks(ctx context.Context, weaponHash int64, columns []models.PerkColumn) (inserted, deleted int64, err error) {
	perkHashes, columnIndexes := columnArrays(columns)

	err = s.pool.QueryRow(ctx, `
		WITH w AS (
			SELECT id FROM weapon WHERE hash = $1
		), incoming AS (
			SELECT p.id AS perk_id, x.column_index
			FROM unnest($2::bigint[], $3::smallint[]) AS x(perk_hash, column_index)
			JOIN perk p ON p.hash = x.perk_hash
		), del AS (
			DELETE FROM weapon_perk wp
			USING w
			WHERE wp.weapon_id = w.id
			  AND NOT EXISTS (
				SELECT 1 FROM incoming i
				WHERE i.perk_id = wp.perk_id AND i.column_index = wp.column_index
			  )
			RETURNING 1
		), ins AS (
			INSERT INTO weapon_perk (weapon_id, perk_id, column_index)
			SELECT w.id, i.perk_id, i.column_index FROM w, incoming i
			ON CONFLICT DO NOTHING
			RETURNING 1
		)
		SELECT (SELECT count(*) FROM ins), (SELECT count(*) FROM del)`,
		weaponHash, perkHashes, columnIndexes,
	).Scan(&inserted, &deleted)
	if err != nil {
		return 0, 0, fmt.Errorf("db: replacing weapon perks for %d: %w", weaponHash, err)
	}
	return inserted, deleted, nil
}

// ReplaceRolls reconciles the roll skeletons for one weapon inside a single
// transaction: stale rolls (combo keys no longer generated) are pruned,
// new rolls are inserted along with their roll_perk rows via COPY, and
// existing rolls — including any curated scores on them — are untouched.
func (s *Store) ReplaceRolls(ctx context.Context, weaponHash int64, rolls []Roll, perkIDs map[int64]int64) (inserted, deleted int64, err error) {
	keys := make([]string, len(rolls))
	byKey := make(map[string]*Roll, len(rolls))
	for i := range rolls {
		keys[i] = rolls[i].ComboKey
		byKey[rolls[i].ComboKey] = &rolls[i]
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("db: beginning roll tx for %d: %w", weaponHash, err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	var weaponID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM weapon WHERE hash = $1`, weaponHash).Scan(&weaponID); err != nil {
		return 0, 0, fmt.Errorf("db: resolving weapon %d for rolls: %w", weaponHash, err)
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM roll WHERE weapon_id = $1 AND combo_key <> ALL($2::text[])`,
		weaponID, keys)
	if err != nil {
		return 0, 0, fmt.Errorf("db: pruning stale rolls for %d: %w", weaponHash, err)
	}
	deleted = tag.RowsAffected()

	rows, err := tx.Query(ctx, `
		INSERT INTO roll (weapon_id, combo_key)
		SELECT $1, ck FROM unnest($2::text[]) AS ck
		ON CONFLICT (weapon_id, combo_key) DO NOTHING
		RETURNING id, combo_key`,
		weaponID, keys)
	if err != nil {
		return 0, 0, fmt.Errorf("db: inserting rolls for %d: %w", weaponHash, err)
	}

	// COPY rows for the roll_perk rows of newly created rolls only;
	// existing rolls already have theirs (roll contents are immutable —
	// a different perk set is a different combo_key).
	var copyRows [][]any
	for rows.Next() {
		var rollID int64
		var comboKey string
		if err := rows.Scan(&rollID, &comboKey); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("db: scanning new roll for %d: %w", weaponHash, err)
		}
		roll, ok := byKey[comboKey]
		if !ok {
			rows.Close()
			return 0, 0, fmt.Errorf("db: database returned unknown combo key %s for weapon %d", comboKey, weaponHash)
		}
		for _, rp := range roll.Perks {
			perkID, ok := perkIDs[rp.PerkHash]
			if !ok {
				rows.Close()
				return 0, 0, fmt.Errorf("db: perk hash %d not present in perk table", rp.PerkHash)
			}
			copyRows = append(copyRows, []any{rollID, perkID, int16(rp.Column)})
		}
		inserted++
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("db: iterating new rolls for %d: %w", weaponHash, err)
	}

	if len(copyRows) > 0 {
		if _, err := tx.CopyFrom(ctx,
			pgx.Identifier{"roll_perk"},
			[]string{"roll_id", "perk_id", "column_index"},
			pgx.CopyFromRows(copyRows),
		); err != nil {
			return 0, 0, fmt.Errorf("db: copying roll perks for %d: %w", weaponHash, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("db: committing rolls for %d: %w", weaponHash, err)
	}
	return inserted, deleted, nil
}

// tallyUpsert converts RETURNING (xmax = 0) rows into insert/update counts;
// rows not returned at all were unchanged (the DO UPDATE WHERE clause
// filtered them out).
func tallyUpsert(rows pgx.Rows, total int) (Counts, error) {
	defer rows.Close()
	var c Counts
	for rows.Next() {
		var isInsert bool
		if err := rows.Scan(&isInsert); err != nil {
			return Counts{}, fmt.Errorf("db: scanning upsert result: %w", err)
		}
		if isInsert {
			c.Inserted++
		} else {
			c.Updated++
		}
	}
	if err := rows.Err(); err != nil {
		return Counts{}, fmt.Errorf("db: reading upsert results: %w", err)
	}
	c.Unchanged = total - c.Inserted - c.Updated
	return c, nil
}
