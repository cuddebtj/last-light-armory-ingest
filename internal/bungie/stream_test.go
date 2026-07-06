package bungie

import (
	"errors"
	"strings"
	"testing"
)

type miniDef struct {
	Name string `json:"name"`
}

func TestStreamDefinitionsHappyPath(t *testing.T) {
	input := `{
		"236342179": {"name": "alpha"},
		"4294967295": {"name": "omega"},
		"1": {"name": "tiny"}
	}`

	got := map[uint32]string{}
	err := StreamDefinitions(strings.NewReader(input), func(hash uint32, d miniDef) error {
		got[hash] = d.Name
		return nil
	})
	if err != nil {
		t.Fatalf("StreamDefinitions: %v", err)
	}
	want := map[uint32]string{236342179: "alpha", 4294967295: "omega", 1: "tiny"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for h, n := range want {
		if got[h] != n {
			t.Errorf("hash %d = %q, want %q", h, got[h], n)
		}
	}
}

func TestStreamDefinitionsEmptyTable(t *testing.T) {
	calls := 0
	err := StreamDefinitions(strings.NewReader(`{}`), func(hash uint32, d miniDef) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("StreamDefinitions: %v", err)
	}
	if calls != 0 {
		t.Errorf("callback called %d times on empty table", calls)
	}
}

func TestStreamDefinitionsCallbackErrorAborts(t *testing.T) {
	sentinel := errors.New("stop here")
	calls := 0
	err := StreamDefinitions(strings.NewReader(`{"1":{"name":"a"},"2":{"name":"b"}}`), func(hash uint32, d miniDef) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("callback called %d times, want 1 (abort on first error)", calls)
	}
}

func TestStreamDefinitionsMalformedInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"not an object", `[1,2,3]`},
		{"empty input", ``},
		{"non-numeric key", `{"not-a-hash":{}}`},
		{"key overflows uint32", `{"4294967296":{}}`},
		{"negative key", `{"-5":{}}`},
		{"truncated value", `{"1":{"name":`},
		{"garbage", `hello`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := StreamDefinitions(strings.NewReader(tt.input), func(hash uint32, d miniDef) error { return nil })
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}
