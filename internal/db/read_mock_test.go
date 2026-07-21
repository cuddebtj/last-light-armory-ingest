package db

import (
	"context"
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

// Fault-injection for the export read queries; happy paths against real
// Postgres live in the integration suite.

func TestReadQueriesErrorPaths(t *testing.T) {
	ctx := context.Background()

	calls := []struct {
		name    string
		sqlHint string
		columns []string
		badRow  []any
		run     func(*Store) error
	}{
		{
			"AllWeapons", "FROM weapon ORDER BY hash",
			[]string{"hash", "name", "weapon_type", "frame", "rpm", "slot", "element", "tier", "source", "icon", "watermark", "ammo_type", "breaker_type", "craftable", "enhanceable", "obtainable"},
			[]any{"not-an-int", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil},
			func(s *Store) error { _, err := s.AllWeapons(ctx); return err },
		},
		{
			"AllPerks", "FROM perk ORDER BY hash",
			[]string{"hash", "name", "enhanced", "icon", "pve_score", "pvp_score"},
			[]any{"not-an-int", nil, nil, nil, nil, nil},
			func(s *Store) error { _, err := s.AllPerks(ctx); return err },
		},
		{
			"AllWeaponPerks", "FROM weapon_perk",
			[]string{"hash", "column_index", "perk_hash"},
			[]any{"not-an-int", nil, nil},
			func(s *Store) error { _, err := s.AllWeaponPerks(ctx); return err },
		},
		{
			"AllRollPerks", "FROM roll",
			[]string{"hash", "id", "combo_key", "pve", "pvp", "overall", "column_index", "perk_hash"},
			[]any{"not-an-int", nil, nil, nil, nil, nil, nil, nil},
			func(s *Store) error { _, err := s.AllRollPerks(ctx); return err },
		},
		{
			"AllWeaponRankings", "FROM weapon_ranking",
			[]string{"hash", "overall_score", "pve_score", "pvp_score", "popularity_score"},
			[]any{"not-an-int", nil, nil, nil, nil},
			func(s *Store) error { _, err := s.AllWeaponRankings(ctx); return err },
		},
	}

	for _, c := range calls {
		t.Run(c.name+" query error", func(t *testing.T) {
			mock, store := newMock(t)
			mock.ExpectQuery(c.sqlHint).WillReturnError(errBoom)
			if err := c.run(store); !errors.Is(err, errBoom) {
				t.Errorf("err = %v", err)
			}
		})

		t.Run(c.name+" scan error", func(t *testing.T) {
			mock, store := newMock(t)
			mock.ExpectQuery(c.sqlHint).WillReturnRows(pgxmock.NewRows(c.columns).AddRow(c.badRow...))
			if err := c.run(store); err == nil {
				t.Error("want scan error")
			}
		})

	}
}

// queryAll is generic and shared by every read method, so one mid-iteration
// failure (good first row, error on the second) covers its rows.Err branch.
func TestReadQueriesRowIterationError(t *testing.T) {
	mock, store := newMock(t)
	mock.ExpectQuery("FROM perk ORDER BY hash").WillReturnRows(
		pgxmock.NewRows([]string{"hash", "name", "enhanced", "icon", "pve_score", "pvp_score"}).
			AddRow(int64(1), "Zen Moment", false, nil, nil, nil).
			AddRow(int64(2), "Rampage", false, nil, nil, nil).
			RowError(1, errBoom))
	if _, err := store.AllPerks(context.Background()); err == nil {
		t.Error("want rows iteration error")
	}
}
