// Command export bakes the static JSON artifacts the website consumes:
// meta.json, perks.json, weapons/index.json, and one weapons/<hash>.json
// per weapon. The database never faces the public internet (it lives on a
// private network); these artifacts are the only data that leaves it —
// commit them where the website build can reach them and deploy.
//
// Usage:
//
//	export [-env PATH] [-out DIR]
//
// Exit codes: 0 success, 1 any failure.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cuddebtj/last-light-armory-ingest/internal/config"
	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/export"
)

func main() {
	os.Exit(run())
}

func run() int {
	envFile := flag.String("env", ".env", "path to .env file (\"\" to rely on real environment only)")
	outDir := flag.String("out", "export", "directory to write JSON artifacts into")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(*envFile)
	if err != nil {
		log.Error("configuration error", "error", err)
		return 1
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection error", "error", err)
		return 1
	}
	defer pool.Close()

	// Sharing the ingest advisory lock means an export never reads while an
	// ingest is mid-reconciliation.
	release, err := db.AcquireIngestLock(ctx, pool)
	if err != nil {
		log.Error("could not acquire lock (is an ingest running?)", "error", err)
		return 1
	}
	defer release()

	store := db.NewStore(pool)

	version, found, err := store.GetSyncState(ctx)
	if err != nil {
		log.Error("reading sync state", "error", err)
		return 1
	}
	if !found {
		log.Error("no ingest has ever completed; nothing to export", "error", errors.New("manifest_sync_state is empty"))
		return 1
	}

	weapons, err := store.AllWeapons(ctx)
	if err != nil {
		log.Error("reading weapons", "error", err)
		return 1
	}
	perks, err := store.AllPerks(ctx)
	if err != nil {
		log.Error("reading perks", "error", err)
		return 1
	}
	links, err := store.AllWeaponPerks(ctx)
	if err != nil {
		log.Error("reading weapon perks", "error", err)
		return 1
	}
	rollRows, err := store.AllRollPerks(ctx)
	if err != nil {
		log.Error("reading rolls", "error", err)
		return 1
	}
	rankings, err := store.AllWeaponRankings(ctx)
	if err != nil {
		log.Error("reading weapon rankings", "error", err)
		return 1
	}

	site, err := export.Build(version, time.Now(), weapons, perks, links, rollRows, rankings)
	if err != nil {
		log.Error("assembling export", "error", err)
		return 1
	}
	if err := site.Write(*outDir); err != nil {
		log.Error("writing artifacts", "error", err)
		return 1
	}

	fmt.Printf("exported manifest %s to %s: %d weapons, %d perks, %d rolls\n",
		version, *outDir, site.Meta.WeaponCount, site.Meta.PerkCount, site.Meta.RollCount)
	return 0
}
