// Package config loads and validates the environment configuration for the
// ingest job.
//
// Configuration comes from process environment variables, optionally seeded
// from a .env file. Real environment variables always win over .env values,
// matching the common twelve-factor precedence so deployments can override
// the file without editing it.
//
// Only two variables are used:
//
//	BUNGIE_API_KEY  — sent as the X-API-Key header on every Bungie request
//	DATABASE_URL    — Postgres connection string for the shared database
//
// The BUNGIE_OAUTH_* variables that appear in .env.example belong to the
// sibling last-light-armory repo. Ingestion only touches public manifest
// data and deliberately never reads them (see CLAUDE.md "Non-Negotiables").
package config

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config holds the validated runtime configuration for one ingest run.
type Config struct {
	// BungieAPIKey is the registered Bungie.net application key, sent as
	// the X-API-Key header on every API request.
	BungieAPIKey string

	// DatabaseURL is the postgres:// connection string, including
	// credentials. Never log this value.
	DatabaseURL string
}

// Load builds a Config from the process environment, first seeding any
// variables found in envFile (pass "" to skip file loading entirely, e.g. in
// production where a secret manager injects real environment variables).
//
// A missing envFile is not an error; a present-but-malformed one is.
func Load(envFile string) (*Config, error) {
	if envFile != "" {
		if err := loadEnvFile(envFile); err != nil {
			return nil, err
		}
	}

	cfg := &Config{
		BungieAPIKey: strings.TrimSpace(os.Getenv("BUNGIE_API_KEY")),
		DatabaseURL:  strings.TrimSpace(os.Getenv("DATABASE_URL")),
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate checks that required values are present and structurally sound.
// It intentionally does not connect to anything; deep validation of the
// database URL happens when the pgx pool is created.
func (c *Config) validate() error {
	if c.BungieAPIKey == "" {
		return fmt.Errorf("config: BUNGIE_API_KEY is required (set it in the environment or .env)")
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL is required (set it in the environment or .env)")
	}

	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		// err may echo parts of the URL; wrap without repeating the raw value.
		return fmt.Errorf("config: DATABASE_URL is not a parseable URL (check that special characters in the password are percent-encoded)")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("config: DATABASE_URL scheme must be postgres:// or postgresql://, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("config: DATABASE_URL has no host (check that special characters in the password are percent-encoded)")
	}
	// No explicit port validation: url.Parse already rejects non-numeric
	// ports (the classic symptom of an unencoded '/' or ':' in the
	// password), which the parse-error branch above reports with the same
	// percent-encoding hint.
	return nil
}

// loadEnvFile reads a .env-style file and sets each variable into the process
// environment unless it is already set there. Supported syntax per line:
//
//	KEY=value
//	export KEY=value
//	KEY="quoted value"   (single or double quotes stripped)
//	# comment            (ignored, as are blank lines)
//
// Values are not interpolated; a .env file is data, not shell.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // optional file
		}
		return fmt.Errorf("config: opening %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("config: %s:%d: expected KEY=value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t") {
			return fmt.Errorf("config: %s:%d: invalid variable name", path, lineNo)
		}

		value = strings.TrimSpace(value)
		value = unquote(value)

		// Real environment always wins over the file.
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("config: setting %s: %w", key, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("config: reading %s: %w", path, err)
	}
	return nil
}

// unquote strips one layer of matching single or double quotes, if present.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
