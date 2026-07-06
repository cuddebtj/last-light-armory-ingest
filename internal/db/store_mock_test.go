package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// These tests fault-inject the paths a live database can't realistically
// produce (scan failures, mid-iteration errors, commit failures, impossible
// result sets). Happy-path behavior against real Postgres is covered by the
// integration suite.

var errBoom = errors.New("boom")

func newMock(t *testing.T) (pgxmock.PgxPoolIface, *Store) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(mock.Close)
	return mock, NewStore(mock)
}

func expectMet(t *testing.T, mock pgxmock.PgxPoolIface) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetSyncStateMock(t *testing.T) {
	ctx := context.Background()

	t.Run("found", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("SELECT last_manifest_version").
			WillReturnRows(pgxmock.NewRows([]string{"last_manifest_version"}).AddRow("v9"))
		v, found, err := store.GetSyncState(ctx)
		if err != nil || !found || v != "v9" {
			t.Errorf("got (%q,%v,%v)", v, found, err)
		}
		expectMet(t, mock)
	})

	t.Run("no rows means not found", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("SELECT last_manifest_version").WillReturnError(pgx.ErrNoRows)
		_, found, err := store.GetSyncState(ctx)
		if err != nil || found {
			t.Errorf("got (found=%v, err=%v), want (false, nil)", found, err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("SELECT last_manifest_version").WillReturnError(errBoom)
		if _, _, err := store.GetSyncState(ctx); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUpsertSyncStateMockError(t *testing.T) {
	mock, store := newMock(t)
	mock.ExpectExec("INSERT INTO manifest_sync_state").WithArgs("v1", true).WillReturnError(errBoom)
	if err := store.UpsertSyncState(context.Background(), "v1", true); !errors.Is(err, errBoom) {
		t.Errorf("err = %v", err)
	}
	expectMet(t, mock)
}

func TestUpsertPerksMock(t *testing.T) {
	ctx := context.Background()
	perks := []models.Perk{{Hash: 1, Name: "A"}, {Hash: 2, Name: "B"}, {Hash: 3, Name: "C"}}

	t.Run("empty batch is a no-op", func(t *testing.T) {
		// nil pool: proves no query is even attempted.
		store := NewStore(nil)
		c, err := store.UpsertPerks(ctx, nil)
		if err != nil || c != (Counts{}) {
			t.Errorf("got %+v, %v", c, err)
		}
	})

	t.Run("counts inserted updated unchanged", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("INSERT INTO perk").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"inserted"}).AddRow(true).AddRow(false))
		c, err := store.UpsertPerks(ctx, perks)
		if err != nil {
			t.Fatal(err)
		}
		if c.Inserted != 1 || c.Updated != 1 || c.Unchanged != 1 {
			t.Errorf("counts = %+v", c)
		}
		expectMet(t, mock)
	})

	t.Run("query error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("INSERT INTO perk").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(errBoom)
		if _, err := store.UpsertPerks(ctx, perks); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("INSERT INTO perk").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"inserted"}).AddRow("not-a-bool"))
		if _, err := store.UpsertPerks(ctx, perks); err == nil {
			t.Error("want scan error")
		}
	})

	t.Run("row iteration error", func(t *testing.T) {
		mock, store := newMock(t)
		// Error on the second row: the first scans fine, then rows.Err()
		// must surface the failure after the loop.
		mock.ExpectQuery("INSERT INTO perk").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"inserted"}).AddRow(true).AddRow(false).RowError(1, errBoom))
		if _, err := store.UpsertPerks(ctx, perks); err == nil {
			t.Error("want rows error")
		}
	})
}

func TestUpsertWeaponsMock(t *testing.T) {
	ctx := context.Background()
	weapons := []models.Weapon{{Hash: 1, Name: "W", WeaponType: "Auto Rifle", Slot: "Kinetic"}}

	t.Run("empty batch is a no-op", func(t *testing.T) {
		store := NewStore(nil)
		c, err := store.UpsertWeapons(ctx, nil)
		if err != nil || c != (Counts{}) {
			t.Errorf("got %+v, %v", c, err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("INSERT INTO weapon").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(errBoom)
		if _, err := store.UpsertWeapons(ctx, weapons); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("happy", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("INSERT INTO weapon").WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"inserted"}).AddRow(true))
		c, err := store.UpsertWeapons(ctx, weapons)
		if err != nil || c.Inserted != 1 {
			t.Errorf("got %+v, %v", c, err)
		}
		expectMet(t, mock)
	})
}

func TestPerkIDsByHashMock(t *testing.T) {
	ctx := context.Background()

	t.Run("query error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("SELECT hash, id FROM perk").WillReturnError(errBoom)
		if _, err := store.PerkIDsByHash(ctx); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("SELECT hash, id FROM perk").
			WillReturnRows(pgxmock.NewRows([]string{"hash", "id"}).AddRow("x", "y"))
		if _, err := store.PerkIDsByHash(ctx); err == nil {
			t.Error("want scan error")
		}
	})

	t.Run("happy", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectQuery("SELECT hash, id FROM perk").
			WillReturnRows(pgxmock.NewRows([]string{"hash", "id"}).AddRow(int64(30), int64(1)))
		ids, err := store.PerkIDsByHash(ctx)
		if err != nil || ids[30] != 1 {
			t.Errorf("got %v, %v", ids, err)
		}
	})
}

func TestReplaceWeaponPerksMockError(t *testing.T) {
	mock, store := newMock(t)
	mock.ExpectQuery("WITH w AS").WithArgs(int64(1000), pgxmock.AnyArg(), pgxmock.AnyArg()).WillReturnError(errBoom)
	_, _, err := store.ReplaceWeaponPerks(context.Background(), 1000, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
	expectMet(t, mock)
}

// rollFixtures returns one roll and a perk-id map resolving its perk.
func rollFixtures() ([]Roll, map[int64]int64) {
	rolls := []Roll{{ComboKey: "ck-1", Perks: []models.RollPerk{{Column: 2, PerkHash: 30}}}}
	return rolls, map[int64]int64{30: 7}
}

func TestReplaceRollsMock(t *testing.T) {
	ctx := context.Background()
	weaponRow := func() *pgxmock.Rows {
		return pgxmock.NewRows([]string{"id"}).AddRow(int64(42))
	}

	t.Run("begin error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin().WillReturnError(errBoom)
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("weapon resolve error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnError(pgx.ErrNoRows)
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); err == nil {
			t.Error("want resolve error")
		}
		expectMet(t, mock)
	})

	t.Run("prune error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnError(errBoom)
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("insert error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnError(errBoom)
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("new roll scan error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).AddRow("bad", "ck-1"))
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); err == nil {
			t.Error("want scan error")
		}
	})

	t.Run("database returns unknown combo key", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).AddRow(int64(5), "never-generated"))
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		_, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids)
		if err == nil || !strings.Contains(err.Error(), "unknown combo key") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("unknown perk hash", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).AddRow(int64(5), "ck-1"))
		mock.ExpectRollback()
		rolls, _ := rollFixtures()
		_, _, err := store.ReplaceRolls(ctx, 1000, rolls, map[int64]int64{}) // empty map
		if err == nil || !strings.Contains(err.Error(), "not present in perk table") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("row iteration error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		// Error on the second row so the first iterates cleanly and the
		// failure lands in rows.Err() after the loop.
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).
				AddRow(int64(5), "ck-1").AddRow(int64(6), "ck-1").RowError(1, errBoom))
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); err == nil {
			t.Error("want rows error")
		}
	})

	t.Run("copy error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).AddRow(int64(5), "ck-1"))
		mock.ExpectCopyFrom(pgx.Identifier{"roll_perk"}, []string{"roll_id", "perk_id", "column_index"}).
			WillReturnError(errBoom)
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("commit error", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).AddRow(int64(5), "ck-1"))
		mock.ExpectCopyFrom(pgx.Identifier{"roll_perk"}, []string{"roll_id", "perk_id", "column_index"}).
			WillReturnResult(1)
		mock.ExpectCommit().WillReturnError(errBoom)
		mock.ExpectRollback()
		rolls, ids := rollFixtures()
		if _, _, err := store.ReplaceRolls(ctx, 1000, rolls, ids); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("happy with new rolls", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 3))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"}).AddRow(int64(5), "ck-1"))
		mock.ExpectCopyFrom(pgx.Identifier{"roll_perk"}, []string{"roll_id", "perk_id", "column_index"}).
			WillReturnResult(1)
		mock.ExpectCommit()
		mock.ExpectRollback() // deferred rollback after commit is a no-op

		rolls, ids := rollFixtures()
		ins, del, err := store.ReplaceRolls(ctx, 1000, rolls, ids)
		if err != nil || ins != 1 || del != 3 {
			t.Errorf("got +%d/-%d, %v", ins, del, err)
		}
	})

	t.Run("happy with nothing new skips copy", func(t *testing.T) {
		mock, store := newMock(t)
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT id FROM weapon WHERE hash").WithArgs(int64(1000)).WillReturnRows(weaponRow())
		mock.ExpectExec("DELETE FROM roll").WithArgs(int64(42), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("DELETE", 0))
		mock.ExpectQuery("INSERT INTO roll").WithArgs(int64(42), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id", "combo_key"})) // no new rolls
		mock.ExpectCommit()
		mock.ExpectRollback()

		rolls, ids := rollFixtures()
		ins, del, err := store.ReplaceRolls(ctx, 1000, rolls, ids)
		if err != nil || ins != 0 || del != 0 {
			t.Errorf("got +%d/-%d, %v", ins, del, err)
		}
	})
}
