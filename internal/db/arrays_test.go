package db

import (
	"testing"

	"github.com/cuddebtj/last-light-armory-ingest/internal/models"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestPerkArrays(t *testing.T) {
	hashes, names, enhanced, icons := perkArrays([]models.Perk{
		{Hash: 1, Name: "Rampage", Enhanced: false, Icon: strPtr("/icons/rampage.jpg")},
		{Hash: 2, Name: "Rampage", Enhanced: true},
	})
	if len(hashes) != 2 || hashes[0] != 1 || hashes[1] != 2 {
		t.Errorf("hashes = %v", hashes)
	}
	if names[0] != "Rampage" || names[1] != "Rampage" {
		t.Errorf("names = %v", names)
	}
	if enhanced[0] || !enhanced[1] {
		t.Errorf("enhanced = %v", enhanced)
	}
	if icons[0] == nil || *icons[0] != "/icons/rampage.jpg" || icons[1] != nil {
		t.Errorf("icons = %v", icons)
	}
}

func TestWeaponArraysNullHandling(t *testing.T) {
	a := weaponArrays([]models.Weapon{
		{
			Hash: 10, Name: "Full", WeaponType: "Auto Rifle",
			Frame: strPtr("Adaptive Frame"), RPM: intPtr(600), Slot: "Kinetic",
			Element: strPtr("Arc"), Tier: strPtr("Legendary"), Source: strPtr("Source: X."),
			Icon: strPtr("/icons/full.jpg"), Watermark: strPtr("/icons/wm.png"),
			AmmoType: strPtr("Primary"), BreakerType: strPtr("Shield Piercing"),
			Craftable: true, Enhanceable: true, Obtainable: true,
		},
		{Hash: 11, Name: "Sparse", WeaponType: "Sword", Slot: "Power"},
	})

	if a.hashes[0] != 10 || a.hashes[1] != 11 {
		t.Errorf("hashes = %v", a.hashes)
	}
	if a.frames[0] == nil || *a.frames[0] != "Adaptive Frame" {
		t.Errorf("frames[0] = %v", a.frames[0])
	}
	if a.rpms[0] == nil || *a.rpms[0] != 600 {
		t.Errorf("rpms[0] = %v", a.rpms[0])
	}
	if a.icons[0] == nil || *a.icons[0] != "/icons/full.jpg" || a.watermarks[0] == nil {
		t.Errorf("icons[0]/watermarks[0] = %v/%v", a.icons[0], a.watermarks[0])
	}
	if a.ammoTypes[0] == nil || *a.ammoTypes[0] != "Primary" || a.breakerTypes[0] == nil || *a.breakerTypes[0] != "Shield Piercing" {
		t.Errorf("ammoTypes[0]/breakerTypes[0] = %v/%v", a.ammoTypes[0], a.breakerTypes[0])
	}
	// Sparse weapon: every nullable field must be nil, not zero-valued.
	if a.frames[1] != nil || a.rpms[1] != nil || a.elements[1] != nil || a.tiers[1] != nil ||
		a.sources[1] != nil || a.icons[1] != nil || a.watermarks[1] != nil ||
		a.ammoTypes[1] != nil || a.breakerTypes[1] != nil {
		t.Errorf("sparse weapon nullables not nil: %+v", a)
	}
	if a.craftable[0] != true || a.craftable[1] != false {
		t.Errorf("craftable = %v", a.craftable)
	}
}

func TestColumnArrays(t *testing.T) {
	perkHashes, columnIndexes := columnArrays([]models.PerkColumn{
		{Index: 0, Perks: []models.Perk{{Hash: 100}, {Hash: 101}}},
		{Index: 3, Perks: []models.Perk{{Hash: 200}}},
	})
	wantHashes := []int64{100, 101, 200}
	wantCols := []int16{0, 0, 3}
	if len(perkHashes) != 3 {
		t.Fatalf("perkHashes = %v", perkHashes)
	}
	for i := range wantHashes {
		if perkHashes[i] != wantHashes[i] || columnIndexes[i] != wantCols[i] {
			t.Errorf("row %d = (%d,%d), want (%d,%d)", i, perkHashes[i], columnIndexes[i], wantHashes[i], wantCols[i])
		}
	}
}

func TestColumnArraysEmpty(t *testing.T) {
	perkHashes, columnIndexes := columnArrays(nil)
	if perkHashes != nil || columnIndexes != nil {
		t.Errorf("want nil slices, got %v / %v", perkHashes, columnIndexes)
	}
}

func TestPgx5URL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"postgresql://u:p@h:5432/d", "pgx5://u:p@h:5432/d"},
		{"postgres://u:p@h/d", "pgx5://u:p@h/d"},
		{"pgx5://already", "pgx5://already"},
		{"mysql://other", "mysql://other"},
	}
	for _, tt := range tests {
		if got := pgx5URL(tt.in); got != tt.want {
			t.Errorf("pgx5URL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
