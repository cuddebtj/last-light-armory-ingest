package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// addFixtureWeapon splices an extra weapon into a fixture API's inventory
// item table, sharing the standard plug fixtures.
func addFixtureWeapon(api *fakeAPI, hash uint32, name string) {
	table := api.components[componentInventoryItem]
	entry := fmt.Sprintf("%q:%s,", fmt.Sprint(hash), weaponJSON(name, 6))
	api.components[componentInventoryItem] = strings.Replace(table, "{", "{"+entry, 1)
}

func TestRunNilLoggerUsesDefault(t *testing.T) {
	api := newFixtureAPI("v-new")
	store := &fakeStore{}
	r := &Runner{API: api, Store: store} // Log deliberately nil

	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run with nil logger: %v", err)
	}
}

func TestRunSkipPathSyncUpsertError(t *testing.T) {
	api := newFixtureAPI("v-same")
	store := &fakeStore{syncVersion: "v-same", syncFound: true, failOn: "UpsertSyncState"}
	r := &Runner{API: api, Store: store, Log: testLogger()}

	if _, err := r.Run(context.Background()); !errors.Is(err, errInjected) {
		t.Fatalf("want injected error from skip-path sync upsert, got %v", err)
	}
}

func TestRunDryRunSkipPathWritesNothing(t *testing.T) {
	api := newFixtureAPI("v-same")
	store := &fakeStore{syncVersion: "v-same", syncFound: true}
	r := &Runner{API: api, Store: store, Log: testLogger(), DryRun: true}

	s, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !s.Skipped {
		t.Error("Skipped = false, want true")
	}
	if len(store.syncUpserts) != 0 {
		t.Errorf("dry-run skip recorded a sync upsert: %+v", store.syncUpserts)
	}
}

// cancelOnWarn is a slog.Handler that cancels a context the first time a
// Warn-level record passes through it. Warn fires synchronously inside a
// worker's processOne (via the roll cap), giving a deterministic point to
// cancel while the feeder is parked sending the next job.
type cancelOnWarn struct{ cancel context.CancelFunc }

func (h cancelOnWarn) Enabled(context.Context, slog.Level) bool { return true }
func (h cancelOnWarn) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		h.cancel()
	}
	return nil
}
func (h cancelOnWarn) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h cancelOnWarn) WithGroup(string) slog.Handler      { return h }

func TestRunCancellationDuringProcessingUnwindsPools(t *testing.T) {
	// Two weapons, one worker, roll cap forcing a Warn on the first weapon:
	// the Warn handler cancels the context while the feeder is blocked
	// handing over the second weapon, so the feeder's ctx-done branch runs
	// and Run must fail with the cancellation instead of hanging.
	api := newFixtureAPI("v-new")
	addFixtureWeapon(api, 2000, "Second Rifle")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Runner{
		API:               api,
		Store:             &fakeStore{},
		Log:               slog.New(cancelOnWarn{cancel: cancel}),
		WeaponWorkers:     1,
		MaxRollsPerWeapon: 1, // fixture weapons generate 4 rolls -> Warn fires
	}

	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want cancellation error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung after mid-processing cancellation")
	}
}
