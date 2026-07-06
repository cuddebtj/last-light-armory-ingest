package bungie

import (
	"strings"
	"testing"
)

func TestStreamDefinitionsTruncatedAfterKey(t *testing.T) {
	// Object opens and then ends mid-stream: the key-token read must fail.
	err := StreamDefinitions(strings.NewReader(`{`), func(hash uint32, d miniDef) error { return nil })
	if err == nil {
		t.Fatal("want error for truncated input after '{'")
	}
}

func TestStreamDefinitionsGarbageWhereKeyExpected(t *testing.T) {
	// More() sees pending input but the key token itself is invalid JSON.
	err := StreamDefinitions(strings.NewReader(`{X`), func(hash uint32, d miniDef) error { return nil })
	if err == nil {
		t.Fatal("want error for invalid key token")
	}
}

func TestStreamDefinitionsMissingClosingBrace(t *testing.T) {
	// Valid entries but the table never closes: the trailing token read
	// surfaces an unexpected-EOF error rather than silently succeeding.
	err := StreamDefinitions(strings.NewReader(`{"1":{"name":"a"}`), func(hash uint32, d miniDef) error { return nil })
	if err == nil {
		t.Fatal("want error for missing closing brace")
	}
}
