package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrValidationExpired = errors.New("inventory validation is expired")
	ErrValidationApplied = errors.New("inventory validation was already applied")
)

type InventoryValidationView struct {
	ValidationID string          `json:"validation_id"`
	SourceHash   string          `json:"source_hash"`
	ValidUntil   time.Time       `json:"valid_until"`
	Diff         inventoryCounts `json:"diff"`
	Errors       []string        `json:"errors"`
}

type existingMeter struct {
	AreaID        string
	Campus        string
	Building      string
	Floor         string
	Room          string
	Excluded      bool
	ExcludeReason string
	Active        bool
}

func SaveInventoryValidation(ctx context.Context, pool *pgxpool.Pool, plan importer.InventoryPlan, ttl time.Duration) (InventoryValidationView, error) {
	existing := make(map[string]existingMeter)
	rows, err := pool.Query(ctx, `
		SELECT meter_no, area_id, campus, building, floor, room, excluded,
			COALESCE(exclude_reason,''), active FROM meters`)
	if err != nil {
		return InventoryValidationView{}, err
	}
	for rows.Next() {
		var meter string
		var item existingMeter
		if err := rows.Scan(&meter, &item.AreaID, &item.Campus, &item.Building, &item.Floor, &item.Room, &item.Excluded, &item.ExcludeReason, &item.Active); err != nil {
			rows.Close()
			return InventoryValidationView{}, err
		}
		existing[meter] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return InventoryValidationView{}, err
	}
	rows.Close()

	diff := inventoryCounts{Total: plan.Total, Duplicate: plan.Duplicate, Invalid: plan.Invalid, Excluded: plan.Excluded}
	seen := make(map[string]struct{}, len(plan.Records))
	for _, record := range plan.Records {
		seen[record.MeterNo] = struct{}{}
		old, ok := existing[record.MeterNo]
		if !ok {
			diff.Added++
			continue
		}
		if old.AreaID != record.AreaID || old.Campus != record.Campus || old.Building != record.Building ||
			old.Floor != record.Floor || old.Room != record.Room || old.Excluded != record.Excluded ||
			old.ExcludeReason != record.ExcludeReason || !old.Active {
			diff.Updated++
		}
	}
	if len(plan.Records) > 0 {
		areaID := plan.Records[0].AreaID
		for meter, old := range existing {
			if old.AreaID == areaID && old.Active {
				if _, ok := seen[meter]; !ok {
					diff.Deactivated++
				}
			}
		}
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		return InventoryValidationView{}, err
	}
	counts, err := json.Marshal(diff)
	if err != nil {
		return InventoryValidationView{}, err
	}
	errorsJSON, err := json.Marshal(plan.Errors)
	if err != nil {
		return InventoryValidationView{}, err
	}
	validUntil := time.Now().Add(ttl)
	result := InventoryValidationView{SourceHash: plan.SourceHash, ValidUntil: validUntil, Diff: diff, Errors: plan.Errors}
	err = pool.QueryRow(ctx, `
		INSERT INTO inventory_imports (
			source_name, source_hash, source_updated_at, valid_until, status, counts, errors, payload
		) VALUES ($1,$2,$3,$4,'validated',$5,$6,$7)
		RETURNING id::text
	`, plan.SourceName, plan.SourceHash, plan.SourceUpdatedAt, validUntil, counts, errorsJSON, payload).Scan(&result.ValidationID)
	return result, err
}

func ApplyInventoryValidation(ctx context.Context, pool *pgxpool.Pool, validationID string) (InventoryImportView, error) {
	var planJSON []byte
	var status, sourceHash string
	var validUntil *time.Time
	err := pool.QueryRow(ctx, `
		SELECT status, source_hash, valid_until, payload
		FROM inventory_imports WHERE id=$1::uuid
	`, validationID).Scan(&status, &sourceHash, &validUntil, &planJSON)
	if err != nil {
		return InventoryImportView{}, err
	}
	if status != "validated" || validUntil == nil || time.Now().After(*validUntil) {
		return InventoryImportView{}, ErrValidationExpired
	}
	var already bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_imports WHERE source_hash=$1 AND status='applied')`, sourceHash).Scan(&already); err != nil {
		return InventoryImportView{}, err
	}
	if already {
		return InventoryImportView{}, ErrValidationApplied
	}
	var plan importer.InventoryPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return InventoryImportView{}, fmt.Errorf("decode validated inventory: %w", err)
	}
	result, err := ApplyInventory(ctx, pool, plan)
	if err != nil {
		return InventoryImportView{}, err
	}
	var view InventoryImportView
	err = pool.QueryRow(ctx, `
		SELECT id::text, source_name, source_hash, source_updated_at, imported_at, status, counts
		FROM inventory_imports WHERE id=$1::uuid
	`, result.ImportID).Scan(&view.ID, &view.SourceName, &view.SourceHash, &view.SourceUpdatedAt, &view.ImportedAt, &view.Status, &view.Counts)
	return view, err
}

func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
