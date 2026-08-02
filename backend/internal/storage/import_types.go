package storage

type InventoryApplyResult struct {
	ImportID    string `json:"import_id"`
	Idempotent  bool   `json:"idempotent"`
	Total       int    `json:"total"`
	Added       int    `json:"added"`
	Updated     int    `json:"updated"`
	Deactivated int    `json:"deactivated"`
	Duplicate   int    `json:"duplicate"`
	Invalid     int    `json:"invalid"`
	Excluded    int    `json:"excluded"`
}

type SnapshotApplyResult struct {
	RunID      string `json:"run_id"`
	Idempotent bool   `json:"idempotent"`
	Total      int    `json:"total"`
	Valid      int    `json:"valid"`
	Stale      int    `json:"stale"`
	Empty      int    `json:"empty"`
	Error      int    `json:"error"`
	ParseError int    `json:"parse_error"`
}
