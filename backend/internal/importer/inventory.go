package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const maxImportBytes = 32 << 20

type InventoryRecord struct {
	AreaID        string
	Campus        string
	Building      string
	Floor         string
	Room          string
	MeterNo       string
	Excluded      bool
	ExcludeReason string
}

type InventoryPlan struct {
	SourceName      string
	SourceHash      string
	SourceUpdatedAt *time.Time
	Records         []InventoryRecord
	Total           int
	Duplicate       int
	Invalid         int
	Excluded        int
	Errors          []string
}

type inventoryDocument struct {
	UpdatedAt string                `json:"updated_at"`
	Count     int                   `json:"count"`
	Records   []inventoryJSONRecord `json:"records"`
}

type inventoryJSONRecord struct {
	Building string `json:"building"`
	Floor    string `json:"floor"`
	Room     string `json:"room"`
	Meter    string `json:"meter"`
}

func ValidateInventory(
	reader io.Reader,
	sourceName, areaID, campus string,
	loc *time.Location,
	excludedBuildings map[string]struct{},
) (InventoryPlan, error) {
	raw, err := readBounded(reader)
	if err != nil {
		return InventoryPlan{}, err
	}
	var doc inventoryDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return InventoryPlan{}, fmt.Errorf("decode inventory JSON: %w", err)
	}
	if len(doc.Records) == 0 {
		return InventoryPlan{}, errors.New("inventory contains no records")
	}
	if loc == nil {
		loc = time.UTC
	}

	sum := sha256.Sum256(raw)
	plan := InventoryPlan{
		SourceName: sourceName,
		SourceHash: hex.EncodeToString(sum[:]),
		Total:      len(doc.Records),
		Records:    make([]InventoryRecord, 0, len(doc.Records)),
		Errors:     []string{},
	}
	if doc.UpdatedAt != "" {
		parsed, parseErr := parseSourceTime(doc.UpdatedAt, loc)
		if parseErr != nil {
			plan.Errors = append(plan.Errors, "invalid updated_at: "+parseErr.Error())
		} else {
			plan.SourceUpdatedAt = &parsed
		}
	}
	if doc.Count != 0 && doc.Count != len(doc.Records) {
		plan.Errors = append(plan.Errors, fmt.Sprintf(
			"declared count %d does not match records length %d", doc.Count, len(doc.Records),
		))
	}

	seen := make(map[string]struct{}, len(doc.Records))
	for index, row := range doc.Records {
		building := strings.TrimSpace(row.Building)
		floor := strings.TrimSpace(row.Floor)
		room := strings.TrimSpace(row.Room)
		meter := strings.TrimSpace(row.Meter)
		if building == "" || floor == "" || room == "" || meter == "" {
			plan.Invalid++
			plan.Errors = append(plan.Errors, fmt.Sprintf("record %d has a missing location or meter", index))
			continue
		}
		if _, exists := seen[meter]; exists {
			plan.Duplicate++
			continue
		}
		seen[meter] = struct{}{}

		_, excluded := excludedBuildings[building]
		record := InventoryRecord{
			AreaID:   areaID,
			Campus:   campus,
			Building: building,
			Floor:    floor,
			Room:     room,
			MeterNo:  meter,
			Excluded: excluded,
		}
		if excluded {
			record.ExcludeReason = "category:" + building
			plan.Excluded++
		}
		plan.Records = append(plan.Records, record)
	}
	return plan, nil
}

func DefaultExcludedBuildings() map[string]struct{} {
	return map[string]struct{}{
		"通信运营商": {},
		"经营":    {},
	}
}

func readBounded(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportBytes {
		return nil, fmt.Errorf("import exceeds %d bytes", maxImportBytes)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("import is empty")
	}
	return data, nil
}

func parseSourceTime(value string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}
