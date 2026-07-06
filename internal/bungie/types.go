package bungie

// This file defines the subset of Bungie.net API and manifest definition
// shapes this project consumes. Field shapes were sanity-checked against a
// live manifest pull (version 244164.26.06.16.2053-2-bnet.65465, 2026-07-06)
// rather than assumed from documentation.
//
// Bungie hashes are unsigned 32-bit identifiers. They are stored as uint32
// here and widened to int64 at the database boundary (Postgres BIGINT).

// apiResponse is the envelope every Platform endpoint wraps its payload in.
type apiResponse[T any] struct {
	Response        T      `json:"Response"`
	ErrorCode       int    `json:"ErrorCode"`
	ErrorStatus     string `json:"ErrorStatus"`
	Message         string `json:"Message"`
	ThrottleSeconds int    `json:"ThrottleSeconds"`
}

// errorCodeSuccess is Bungie's PlatformErrorCodes.Success.
const errorCodeSuccess = 1

// Manifest is the payload of GET /Destiny2/Manifest/.
type Manifest struct {
	// Version changes whenever Bungie publishes new definition content.
	// Comparing it against manifest_sync_state.last_manifest_version is the
	// cheap "did anything change" check.
	Version string `json:"version"`

	// JSONWorldComponentContentPaths maps locale -> component name ->
	// CDN path (rooted at https://www.bungie.net) for the per-table JSON
	// definition files.
	JSONWorldComponentContentPaths map[string]map[string]string `json:"jsonWorldComponentContentPaths"`
}

// ComponentPath returns the CDN path for one definition table in the given
// locale, e.g. ComponentPath("en", "DestinyInventoryItemDefinition").
// The boolean is false when the locale or component is absent.
func (m *Manifest) ComponentPath(locale, component string) (string, bool) {
	byComponent, ok := m.JSONWorldComponentContentPaths[locale]
	if !ok {
		return "", false
	}
	p, ok := byComponent[component]
	return p, ok
}

// DisplayProperties is the common name/description block on definitions.
type DisplayProperties struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Item type / sub-type constants from Bungie's DestinyItemType and
// DestinyItemSubType enums (only the values this project branches on).
const (
	// ItemTypeWeapon is DestinyItemType.Weapon. In the current manifest all
	// itemType==3 entries are real weapons: zero redacted, blacklisted, or
	// dummy-categorized items (verified live 2026-07-06).
	ItemTypeWeapon = 3
)

// Socket category hashes (DestinySocketCategoryDefinition), verified live:
//
//	3956125808 -> "INTRINSIC TRAITS" (the frame socket)
//	4241085061 -> "WEAPON PERKS"     (barrels, mags, traits, origin trait)
const (
	SocketCategoryIntrinsic   uint32 = 3956125808
	SocketCategoryWeaponPerks uint32 = 4241085061
)

// Weapon stat hashes (DestinyStatDefinition), verified live. RoundsPerMinute
// covers most weapon types; bows, fusions/linear fusions, and swords express
// their "speed" through the other three.
const (
	StatRoundsPerMinute uint32 = 4284893193
	StatChargeTime      uint32 = 2961396640
	StatDrawTime        uint32 = 447667954
	StatSwingSpeed      uint32 = 2837207746
)

// Inventory bucket hashes (DestinyInventoryBucketDefinition) for the three
// weapon slots.
const (
	BucketKinetic uint32 = 1498876634
	BucketEnergy  uint32 = 2465295065
	BucketPower   uint32 = 953998645
)

// InventoryItemDefinition is the subset of DestinyInventoryItemDefinition
// used for weapons and perk plugs.
type InventoryItemDefinition struct {
	DisplayProperties   DisplayProperties `json:"displayProperties"`
	ItemType            int               `json:"itemType"`
	ItemSubType         int               `json:"itemSubType"`
	ItemTypeDisplayName string            `json:"itemTypeDisplayName"`
	Redacted            bool              `json:"redacted"`
	Blacklisted         bool              `json:"blacklisted"`

	Inventory struct {
		TierTypeName   string `json:"tierTypeName"`
		BucketTypeHash uint32 `json:"bucketTypeHash"`
		// RecipeItemHash is set (non-zero) only on craftable weapons; it
		// points at the crafting recipe item.
		RecipeItemHash uint32 `json:"recipeItemHash"`
	} `json:"inventory"`

	// CollectibleHash links to DestinyCollectibleDefinition when the item
	// appears in Collections; zero when absent (348 of 2208 weapons in the
	// current manifest have none).
	CollectibleHash uint32 `json:"collectibleHash"`

	DefaultDamageTypeHash uint32 `json:"defaultDamageTypeHash"`

	Stats struct {
		// Keyed by stat hash rendered as a decimal string.
		Stats map[string]struct {
			Value int `json:"value"`
		} `json:"stats"`
	} `json:"stats"`

	Sockets struct {
		SocketEntries []SocketEntry `json:"socketEntries"`
		// SocketCategories groups socket indexes by purpose; the entry whose
		// SocketCategoryHash is SocketCategoryWeaponPerks lists the real perk
		// columns, in column order.
		SocketCategories []struct {
			SocketCategoryHash uint32 `json:"socketCategoryHash"`
			SocketIndexes      []int  `json:"socketIndexes"`
		} `json:"socketCategories"`
	} `json:"sockets"`
}

// SocketEntry is one socket on an item. Exactly one of
// RandomizedPlugSetHash / ReusablePlugSetHash is normally set for perk
// sockets; random-roll columns use the former, curated columns (e.g. origin
// traits) the latter.
type SocketEntry struct {
	SocketTypeHash        uint32 `json:"socketTypeHash"`
	SingleInitialItemHash uint32 `json:"singleInitialItemHash"`
	ReusablePlugSetHash   uint32 `json:"reusablePlugSetHash"`
	RandomizedPlugSetHash uint32 `json:"randomizedPlugSetHash"`
}

// PlugSetDefinition is the subset of DestinyPlugSetDefinition listing which
// plug items a socket can hold.
type PlugSetDefinition struct {
	ReusablePlugItems []struct {
		PlugItemHash uint32 `json:"plugItemHash"`
		// CurrentlyCanRoll is false for retired perks that existing items may
		// still have but new drops can no longer get; roll generation only
		// considers plugs where this is true.
		CurrentlyCanRoll bool `json:"currentlyCanRoll"`
	} `json:"reusablePlugItems"`
}

// DamageTypeDefinition is the subset of DestinyDamageTypeDefinition needed
// to resolve a weapon's element name.
type DamageTypeDefinition struct {
	DisplayProperties DisplayProperties `json:"displayProperties"`
}

// CollectibleDefinition is the subset of DestinyCollectibleDefinition that
// feeds the obtainability heuristic and the weapon source string.
type CollectibleDefinition struct {
	SourceString string `json:"sourceString"`
	Redacted     bool   `json:"redacted"`
	Blacklisted  bool   `json:"blacklisted"`
}
