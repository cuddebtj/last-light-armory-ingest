package bungie

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// StreamDefinitions incrementally decodes a manifest definition table of the
// form {"<hash>": {definition}, ...} and invokes fn for each entry.
//
// DestinyInventoryItemDefinition alone is ~200 MB of JSON; decoding it into
// one map would hold the entire file in memory at once. Streaming entry by
// entry keeps peak memory proportional to a single definition plus whatever
// the callback chooses to retain.
//
// Returning an error from fn aborts the stream and propagates that error.
func StreamDefinitions[T any](r io.Reader, fn func(hash uint32, def T) error) error {
	dec := json.NewDecoder(r)

	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("bungie: reading definition table opening token: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("bungie: definition table must start with '{', got %v", tok)
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("bungie: reading definition key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("bungie: definition key is not a string: %v", keyTok)
		}
		hash, err := strconv.ParseUint(key, 10, 32)
		if err != nil {
			return fmt.Errorf("bungie: definition key %q is not a uint32 hash: %w", key, err)
		}

		var def T
		if err := dec.Decode(&def); err != nil {
			return fmt.Errorf("bungie: decoding definition %s: %w", key, err)
		}
		if err := fn(uint32(hash), def); err != nil {
			return err
		}
	}

	// A complete table always yields its closing '}' here; any error —
	// including a bare io.EOF from truncated input — means the download was
	// cut short and must not be treated as a successful (partial!) import.
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("bungie: reading definition table closing token: %w", err)
	}
	return nil
}
