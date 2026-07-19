package rolls

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

// col builds a PerkColumn from an index and perk hashes.
func col(index int, hashes ...int64) models.PerkColumn {
	c := models.PerkColumn{Index: index}
	for _, h := range hashes {
		c.Perks = append(c.Perks, models.Perk{Hash: h})
	}
	return c
}

// collect runs Generate and returns each combo as a "col:hash,col:hash"
// string, copying since Generate reuses its buffer.
func collect(t *testing.T, columns []models.PerkColumn) []string {
	t.Helper()
	var out []string
	err := Generate(columns, func(combo []models.RollPerk) error {
		s := ""
		for i, rp := range combo {
			if i > 0 {
				s += ","
			}
			s += fmt.Sprintf("%d:%d", rp.Column, rp.PerkHash)
		}
		out = append(out, s)
		return nil
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return out
}

func TestGenerateCartesianProduct(t *testing.T) {
	got := collect(t, []models.PerkColumn{
		col(0, 10, 11),
		col(1, 20, 21, 22),
	})
	want := []string{
		"0:10,1:20", "0:10,1:21", "0:10,1:22",
		"0:11,1:20", "0:11,1:21", "0:11,1:22",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d combos, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("combo[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGenerateSingleColumn(t *testing.T) {
	got := collect(t, []models.PerkColumn{col(3, 7, 8)})
	want := []string{"3:7", "3:8"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGenerateColumnOrderIndependence(t *testing.T) {
	a := collect(t, []models.PerkColumn{col(0, 1, 2), col(1, 5)})
	b := collect(t, []models.PerkColumn{col(1, 5), col(0, 1, 2)})
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("combo[%d]: %q vs %q — output must not depend on input column order", i, a[i], b[i])
		}
	}
}

func TestGenerateSkipsEmptyColumns(t *testing.T) {
	got := collect(t, []models.PerkColumn{col(0), col(1, 9)})
	if len(got) != 1 || got[0] != "1:9" {
		t.Errorf("got %v, want [1:9]", got)
	}
}

func TestGenerateNoColumns(t *testing.T) {
	calls := 0
	err := Generate(nil, func([]models.RollPerk) error { calls++; return nil })
	if err != nil || calls != 0 {
		t.Errorf("err=%v calls=%d, want nil/0", err, calls)
	}
}

func TestGenerateYieldErrorAborts(t *testing.T) {
	sentinel := errors.New("stop")
	calls := 0
	err := Generate([]models.PerkColumn{col(0, 1, 2, 3)}, func([]models.RollPerk) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel, got %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestGenerateCountAgreement(t *testing.T) {
	tests := [][]models.PerkColumn{
		nil,
		{col(0)},
		{col(0, 1)},
		{col(0, 1, 2), col(1, 3, 4, 5), col(2, 6)},
		{col(0, 1, 2), col(1), col(2, 6, 7)}, // empty middle column
	}
	for i, columns := range tests {
		n := 0
		if err := Generate(columns, func([]models.RollPerk) error { n++; return nil }); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if int64(n) != Count(columns) {
			t.Errorf("case %d: generated %d, Count says %d", i, n, Count(columns))
		}
	}
}

func TestComboKeyDeterministicAndOrderInsensitive(t *testing.T) {
	a := ComboKey([]models.RollPerk{{Column: 0, PerkHash: 10}, {Column: 1, PerkHash: 20}})
	b := ComboKey([]models.RollPerk{{Column: 1, PerkHash: 20}, {Column: 0, PerkHash: 10}})
	if a != b {
		t.Errorf("key depends on input order: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Errorf("key length = %d, want 64 hex chars", len(a))
	}
}

func TestComboKeyDistinguishesRolls(t *testing.T) {
	keys := map[string]string{}
	cases := [][]models.RollPerk{
		{{Column: 0, PerkHash: 10}, {Column: 1, PerkHash: 20}},
		{{Column: 0, PerkHash: 10}, {Column: 1, PerkHash: 21}},
		{{Column: 0, PerkHash: 20}, {Column: 1, PerkHash: 10}}, // same perks, swapped columns
		{{Column: 0, PerkHash: 10}},
		// Adjacent-field ambiguity probe: (1,23) vs (12,3) must differ.
		{{Column: 1, PerkHash: 23}},
		{{Column: 12, PerkHash: 3}},
	}
	for i, c := range cases {
		k := ComboKey(c)
		if prev, dup := keys[k]; dup {
			t.Errorf("case %d collides with %s (key %s)", i, prev, k)
		}
		keys[k] = fmt.Sprintf("case %d", i)
	}
}

func BenchmarkGenerate12x12x3(b *testing.B) {
	mk := func(index, n int) models.PerkColumn {
		c := models.PerkColumn{Index: index}
		for i := 0; i < n; i++ {
			c.Perks = append(c.Perks, models.Perk{Hash: int64(index*1000 + i)})
		}
		return c
	}
	columns := []models.PerkColumn{mk(0, 12), mk(1, 12), mk(2, 3)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Generate(columns, func([]models.RollPerk) error { return nil })
	}
}
