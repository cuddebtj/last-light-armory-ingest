package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

func strPtr(s string) *string   { return &s }
func intPtr(i int) *int         { return &i }
func f64Ptr(f float64) *float64 { return &f }
func i16Ptr(i int16) *int16     { return &i }

var testTime = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

// fixture returns inputs describing two weapons: 1000 with two perk columns
// and two rolls, 1001 with nothing.
func fixture() ([]models.Weapon, []db.PerkRow, []db.WeaponPerkRow, []db.RollPerkRow) {
	weapons := []models.Weapon{
		{Hash: 1000, Name: "Test Rifle", WeaponType: "Auto Rifle", Slot: "Kinetic",
			Element: strPtr("Arc"), Tier: strPtr("Legendary"), Frame: strPtr("Adaptive Frame"),
			RPM: intPtr(600), Craftable: true, Enhanceable: true, Obtainable: true,
			Source: strPtr("Source: Testing."),
			Icon:   strPtr("/icons/rifle.jpg"), Watermark: strPtr("/icons/season.png")},
		{Hash: 1001, Name: "Bare Sword", WeaponType: "Sword", Slot: "Power"},
	}
	perks := []db.PerkRow{
		{Hash: 30, Name: "Zen Moment", Icon: strPtr("/icons/zen.jpg"), PvEScore: i16Ptr(8)},
		{Hash: 31, Name: "Rampage"},
		{Hash: 40, Name: "Veist Stinger"},
	}
	links := []db.WeaponPerkRow{
		{WeaponHash: 1000, ColumnIndex: 0, PerkHash: 30},
		{WeaponHash: 1000, ColumnIndex: 0, PerkHash: 31},
		{WeaponHash: 1000, ColumnIndex: 3, PerkHash: 40},
	}
	rollRows := []db.RollPerkRow{
		// combo "aaa" (roll id 7): two perks; scored.
		{WeaponHash: 1000, RollID: 7, ComboKey: "aaa", OverallScore: f64Ptr(9.1), ColumnIndex: 0, PerkHash: 30},
		{WeaponHash: 1000, RollID: 7, ComboKey: "aaa", OverallScore: f64Ptr(9.1), ColumnIndex: 3, PerkHash: 40},
		// combo "bbb" (roll id 5): one perk; unscored.
		{WeaponHash: 1000, RollID: 5, ComboKey: "bbb", ColumnIndex: 0, PerkHash: 31},
	}
	return weapons, perks, links, rollRows
}

func TestBuildAssemblesSite(t *testing.T) {
	weapons, perks, links, rollRows := fixture()
	site, err := Build("v-test", testTime, weapons, perks, links, rollRows)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if site.Meta.ManifestVersion != "v-test" || !site.Meta.GeneratedAt.Equal(testTime) {
		t.Errorf("meta = %+v", site.Meta)
	}
	if site.Meta.WeaponCount != 2 || site.Meta.PerkCount != 3 || site.Meta.RollCount != 2 {
		t.Errorf("meta counts = %+v", site.Meta)
	}
	if len(site.Perks) != 3 || site.Perks[0].PvEScore == nil || *site.Perks[0].PvEScore != 8 {
		t.Errorf("perks = %+v", site.Perks)
	}
	if site.Perks[0].Icon == nil || *site.Perks[0].Icon != "/icons/zen.jpg" || site.Perks[1].Icon != nil {
		t.Errorf("perk icons = %+v", site.Perks[:2])
	}
	if site.Index[0].Icon == nil || *site.Index[0].Icon != "/icons/rifle.jpg" {
		t.Errorf("index icon = %v", site.Index[0].Icon)
	}
	if site.Index[0].Watermark == nil || *site.Index[0].Watermark != "/icons/season.png" {
		t.Errorf("index watermark = %v", site.Index[0].Watermark)
	}
	if len(site.Index) != 2 || len(site.Docs) != 2 {
		t.Fatalf("index/docs = %d/%d", len(site.Index), len(site.Docs))
	}

	rifle := site.Docs[0]
	if rifle.Hash != 1000 || rifle.RollCount != 2 || site.Index[0].RollCount != 2 {
		t.Errorf("rifle summary = %+v", rifle.WeaponSummary)
	}
	if len(rifle.Columns) != 2 || rifle.Columns[0].Index != 0 || len(rifle.Columns[0].Perks) != 2 || rifle.Columns[1].Index != 3 {
		t.Errorf("rifle columns = %+v", rifle.Columns)
	}
	// Rolls sorted by combo key, not database row order (5 came before 7).
	if rifle.Rolls[0].Key != "aaa" || rifle.Rolls[1].Key != "bbb" {
		t.Errorf("roll order = %q, %q", rifle.Rolls[0].Key, rifle.Rolls[1].Key)
	}
	if len(rifle.Rolls[0].Perks) != 2 || rifle.Rolls[0].Overall == nil || *rifle.Rolls[0].Overall != 9.1 {
		t.Errorf("roll aaa = %+v", rifle.Rolls[0])
	}
	if rifle.Rolls[1].Overall != nil {
		t.Errorf("roll bbb overall = %v, want nil", rifle.Rolls[1].Overall)
	}

	sword := site.Docs[1]
	if sword.Hash != 1001 || len(sword.Columns) != 0 || len(sword.Rolls) != 0 || sword.RollCount != 0 {
		t.Errorf("sword doc = %+v", sword)
	}
}

func TestBuildRejectsOrphanRows(t *testing.T) {
	weapons, perks, links, rollRows := fixture()

	t.Run("orphan link", func(t *testing.T) {
		bad := append([]db.WeaponPerkRow{}, links...)
		bad = append(bad, db.WeaponPerkRow{WeaponHash: 9999, ColumnIndex: 0, PerkHash: 30})
		if _, err := Build("v", testTime, weapons, perks, bad, rollRows); err == nil || !strings.Contains(err.Error(), "9999") {
			t.Fatalf("want orphan-link error, got %v", err)
		}
	})

	t.Run("orphan roll", func(t *testing.T) {
		bad := append([]db.RollPerkRow{}, rollRows...)
		bad = append(bad, db.RollPerkRow{WeaponHash: 8888, RollID: 99, ComboKey: "x", ColumnIndex: 0, PerkHash: 30})
		if _, err := Build("v", testTime, weapons, perks, links, bad); err == nil || !strings.Contains(err.Error(), "8888") {
			t.Fatalf("want orphan-roll error, got %v", err)
		}
	})
}

func TestBuildDeterministic(t *testing.T) {
	weapons, perks, links, rollRows := fixture()
	a, err := Build("v", testTime, weapons, perks, links, rollRows)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build("v", testTime, weapons, perks, links, rollRows)
	if err != nil {
		t.Fatal(err)
	}
	aj, _ := json.Marshal(a.Docs)
	bj, _ := json.Marshal(b.Docs)
	if string(aj) != string(bj) {
		t.Error("two builds over identical input differ")
	}
}

func TestWriteProducesArtifacts(t *testing.T) {
	weapons, perks, links, rollRows := fixture()
	site, err := Build("v-test", testTime, weapons, perks, links, rollRows)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := site.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Every artifact exists and is valid JSON of the right shape.
	var meta Meta
	mustDecode(t, filepath.Join(dir, "meta.json"), &meta)
	if meta.ManifestVersion != "v-test" || meta.RollCount != 2 {
		t.Errorf("meta.json = %+v", meta)
	}

	var perksOut []Perk
	mustDecode(t, filepath.Join(dir, "perks.json"), &perksOut)
	if len(perksOut) != 3 {
		t.Errorf("perks.json has %d entries", len(perksOut))
	}

	var index []WeaponSummary
	mustDecode(t, filepath.Join(dir, "weapons", "index.json"), &index)
	if len(index) != 2 || index[0].Hash != 1000 {
		t.Errorf("index.json = %+v", index)
	}

	var doc WeaponDoc
	mustDecode(t, filepath.Join(dir, "weapons", "1000.json"), &doc)
	if doc.Name != "Test Rifle" || len(doc.Rolls) != 2 {
		t.Errorf("1000.json = %+v", doc)
	}
	mustDecode(t, filepath.Join(dir, "weapons", "1001.json"), &doc)
	if doc.Name != "Bare Sword" {
		t.Errorf("1001.json = %+v", doc)
	}
}

func TestWriteErrors(t *testing.T) {
	weapons, perks, links, rollRows := fixture()
	site, err := Build("v", testTime, weapons, perks, links, rollRows)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("dir path is a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := site.Write(path); err == nil {
			t.Fatal("want mkdir error when target is a file")
		}
	})

	t.Run("unwritable dir", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("permission bits do not bind root")
		}
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "weapons"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o555); err != nil { // weapons/ exists, dir itself read-only
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		if err := site.Write(dir); err == nil {
			t.Fatal("want write error in read-only dir")
		}
	})

	// Each artifact write has its own error return; block them one at a
	// time by planting a directory where a file must go.
	t.Run("perks.json blocked", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "perks.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := site.Write(dir); err == nil || !strings.Contains(err.Error(), "perks.json") {
			t.Fatalf("want perks.json write error, got %v", err)
		}
	})

	t.Run("index.json blocked", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "weapons", "index.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := site.Write(dir); err == nil || !strings.Contains(err.Error(), "index.json") {
			t.Fatalf("want index.json write error, got %v", err)
		}
	})

	t.Run("weapon doc blocked", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "weapons", "1000.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := site.Write(dir); err == nil || !strings.Contains(err.Error(), "1000.json") {
			t.Fatalf("want weapon doc write error, got %v", err)
		}
	})
}

func TestWriteJSONMarshalError(t *testing.T) {
	// A NaN float is unmarshalable; the error path must wrap it.
	err := writeJSON(filepath.Join(t.TempDir(), "bad.json"), map[string]float64{"x": nan()})
	if err == nil || !strings.Contains(err.Error(), "marshaling") {
		t.Fatalf("want marshal error, got %v", err)
	}
}

func nan() float64 { z := 0.0; return z / z }

func mustDecode(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
}
