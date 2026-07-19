package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsControlCharacterInURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("BUNGIE_API_KEY", "k")
	t.Setenv("DATABASE_URL", "postgres://u:p@h:5432/d\x7f")
	if _, err := Load(""); err == nil {
		t.Fatal("want URL parse error for control character")
	}
}

func TestLoadUnreadableEnvFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission bits do not bind root")
	}
	clearEnv(t)
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("BUNGIE_API_KEY=x\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "opening") {
		t.Fatalf("want open error, got %v", err)
	}
}

func TestLoadNulByteInValue(t *testing.T) {
	clearEnv(t)
	// os.Setenv rejects NUL bytes; the loader must surface that instead of
	// silently dropping the variable.
	path := writeEnvFile(t, "BUNGIE_API_KEY=va\x00lue\n")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "setting") {
		t.Fatalf("want setenv error, got %v", err)
	}
}

func TestLoadOverlongEnvLine(t *testing.T) {
	clearEnv(t)
	// bufio.Scanner's default token limit is 64 KiB; a longer line must
	// surface a scan error instead of silently truncating the value.
	long := "BUNGIE_API_KEY=" + strings.Repeat("x", 70*1024) + "\n"
	path := writeEnvFile(t, long)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "reading") {
		t.Fatalf("want scanner error, got %v", err)
	}
}
