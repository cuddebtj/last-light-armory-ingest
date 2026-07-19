// Command ingest runs one check-and-import pass against the shared
// last_light_armory database: it compares the live Bungie manifest version
// with the last imported one and, when they differ (or -force is given),
// re-imports weapons, perk pools, and roll skeletons. It runs, does its
// work, and exits — scheduling belongs to cron or a systemd timer.
//
// Usage:
//
//	ingest [-env PATH] [-force] [-dry-run] [-verbose] [-json]
//
// Exit codes: 0 success (including "version unchanged, skipped"),
// 1 any failure.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cuddebtj/last-light-armory-ingest/internal/bungie"
	"github.com/cuddebtj/last-light-armory-ingest/internal/config"
	"github.com/cuddebtj/last-light-armory-ingest/internal/db"
	"github.com/cuddebtj/last-light-armory-ingest/internal/ingest"
)

func main() {
	os.Exit(run())
}

// run is separated from main so deferred cleanup executes before the
// process exits with a status code.
func run() int {
	envFile := flag.String("env", ".env", "path to .env file (\"\" to rely on real environment only)")
	force := flag.Bool("force", false, "import even when the manifest version is unchanged")
	dryRun := flag.Bool("dry-run", false, "download and process but write nothing to the database")
	verbose := flag.Bool("verbose", false, "log at debug level")
	jsonLogs := flag.Bool("json", false, "emit structured JSON logs instead of text")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	var handler slog.Handler
	if *jsonLogs {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	}
	log := slog.New(handler)

	// SIGINT/SIGTERM cancel the context; every stage of the pipeline is
	// context-aware, so shutdown is prompt and the advisory lock releases.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(*envFile)
	if err != nil {
		log.Error("configuration error", "error", err)
		return 1
	}

	client, err := bungie.New(cfg.BungieAPIKey)
	if err != nil {
		log.Error("bungie client error", "error", err)
		return 1
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection error", "error", err)
		return 1
	}
	defer pool.Close()

	if !*dryRun {
		if err := db.Migrate(cfg.DatabaseURL); err != nil {
			log.Error("migration error", "error", err)
			return 1
		}
		log.Debug("migrations up to date")
	}

	release, err := db.AcquireIngestLock(ctx, pool)
	if err != nil {
		log.Error("could not acquire ingest lock", "error", err)
		return 1
	}
	defer release()

	runner := &ingest.Runner{
		API:    client,
		Store:  db.NewStore(pool),
		Log:    log,
		Force:  *force,
		DryRun: *dryRun,
	}
	summary, err := runner.Run(ctx)
	if err != nil {
		log.Error("ingest failed", "error", err)
		return 1
	}

	// A short human-readable line on stdout; details are in the logs.
	switch {
	case summary.Skipped:
		fmt.Printf("manifest %s unchanged; nothing to do\n", summary.ManifestVersion)
	case summary.DryRun:
		fmt.Printf("dry run: %d weapons, %d rolls would be reconciled (manifest %s)\n",
			summary.WeaponsSeen, summary.RollsSeen, summary.ManifestVersion)
	default:
		fmt.Printf("imported manifest %s: %d weapons (%d new), %d rolls added, %d rolls pruned in %s\n",
			summary.ManifestVersion, summary.WeaponsSeen, summary.Weapons.Inserted,
			summary.RollsAdded, summary.RollsRemoved, summary.Duration.Round(1e9))
	}
	return 0
}
