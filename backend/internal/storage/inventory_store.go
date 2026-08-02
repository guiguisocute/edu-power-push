package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type inventoryCounts struct {
	Total       int `json:"total"`
	Added       int `json:"added"`
	Updated     int `json:"updated"`
	Deactivated int `json:"deactivated"`
	Duplicate   int `json:"duplicate"`
	Invalid     int `json:"invalid"`
	Excluded    int `json:"excluded"`
}

func ApplyInventory(
	ctx context.Context,
	pool *pgxpool.Pool,
	plan importer.InventoryPlan,
) (InventoryApplyResult, error) {
	if len(plan.Records) == 0 {
		return InventoryApplyResult{}, errors.New("inventory plan has no usable records")
	}
	if plan.Invalid > 0 || plan.Duplicate > 0 || len(plan.Errors) > 0 {
		return InventoryApplyResult{}, fmt.Errorf(
			"inventory plan is not clean: invalid=%d duplicate=%d errors=%d",
			plan.Invalid, plan.Duplicate, len(plan.Errors),
		)
	}

	var result InventoryApplyResult
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var existingCounts []byte
		err := tx.QueryRow(ctx, `
			SELECT id::text, counts
			FROM inventory_imports
			WHERE source_hash = $1 AND status = 'applied'
		`, plan.SourceHash).Scan(&result.ImportID, &existingCounts)
		if err == nil {
			var counts inventoryCounts
			if err := json.Unmarshal(existingCounts, &counts); err != nil {
				return fmt.Errorf("decode existing inventory counts: %w", err)
			}
			result.Total = counts.Total
			result.Added = counts.Added
			result.Updated = counts.Updated
			result.Deactivated = counts.Deactivated
			result.Duplicate = counts.Duplicate
			result.Invalid = counts.Invalid
			result.Excluded = counts.Excluded
			result.Idempotent = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check existing inventory import: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			CREATE TEMP TABLE inventory_stage (
				area_id text NOT NULL,
				campus text NOT NULL,
				building text NOT NULL,
				floor text NOT NULL,
				room text NOT NULL,
				meter_no text NOT NULL,
				excluded boolean NOT NULL,
				exclude_reason text
			) ON COMMIT DROP
		`); err != nil {
			return fmt.Errorf("create inventory staging table: %w", err)
		}
		rows := make([][]any, 0, len(plan.Records))
		for _, record := range plan.Records {
			rows = append(rows, []any{
				record.AreaID,
				record.Campus,
				record.Building,
				record.Floor,
				record.Room,
				record.MeterNo,
				record.Excluded,
				nullableText(record.ExcludeReason),
			})
		}
		if _, err := tx.CopyFrom(
			ctx,
			pgx.Identifier{"inventory_stage"},
			[]string{"area_id", "campus", "building", "floor", "room", "meter_no", "excluded", "exclude_reason"},
			pgx.CopyFromRows(rows),
		); err != nil {
			return fmt.Errorf("copy inventory staging rows: %w", err)
		}

		result.Total = plan.Total
		result.Duplicate = plan.Duplicate
		result.Invalid = plan.Invalid
		result.Excluded = plan.Excluded
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM inventory_stage s
			LEFT JOIN meters m ON m.meter_no = s.meter_no
			WHERE m.id IS NULL
		`).Scan(&result.Added); err != nil {
			return fmt.Errorf("count added meters: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM inventory_stage s
			JOIN meters m ON m.meter_no = s.meter_no
			WHERE (m.area_id, m.campus, m.building, m.floor, m.room, m.excluded, m.exclude_reason, m.active)
			      IS DISTINCT FROM
			      (s.area_id, s.campus, s.building, s.floor, s.room, s.excluded, s.exclude_reason, true)
		`).Scan(&result.Updated); err != nil {
			return fmt.Errorf("count updated meters: %w", err)
		}
		areaID := plan.Records[0].AreaID
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM meters m
			WHERE m.area_id = $1
			  AND m.active
			  AND NOT EXISTS (SELECT 1 FROM inventory_stage s WHERE s.meter_no = m.meter_no)
		`, areaID).Scan(&result.Deactivated); err != nil {
			return fmt.Errorf("count deactivated meters: %w", err)
		}

		counts, err := json.Marshal(inventoryCounts{
			Total:       result.Total,
			Added:       result.Added,
			Updated:     result.Updated,
			Deactivated: result.Deactivated,
			Duplicate:   result.Duplicate,
			Invalid:     result.Invalid,
			Excluded:    result.Excluded,
		})
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO inventory_imports (
				source_name, source_hash, source_updated_at, status, counts, errors, applied_at
			)
			VALUES ($1, $2, $3, 'applied', $4, '[]'::jsonb, now())
			RETURNING id::text
		`, plan.SourceName, plan.SourceHash, plan.SourceUpdatedAt, counts).Scan(&result.ImportID); err != nil {
			return fmt.Errorf("insert inventory import: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO meters (
				area_id, campus, building, floor, room, meter_no,
				active, excluded, exclude_reason, source_import_id
			)
			SELECT
				area_id, campus, building, floor, room, meter_no,
				true, excluded, exclude_reason, $1::uuid
			FROM inventory_stage
			ON CONFLICT (meter_no) DO UPDATE SET
				area_id = EXCLUDED.area_id,
				campus = EXCLUDED.campus,
				building = EXCLUDED.building,
				floor = EXCLUDED.floor,
				room = EXCLUDED.room,
				active = true,
				excluded = EXCLUDED.excluded,
				exclude_reason = EXCLUDED.exclude_reason,
				source_import_id = EXCLUDED.source_import_id,
				updated_at = now()
		`, result.ImportID); err != nil {
			return fmt.Errorf("upsert meters: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE meters m
			SET active = false, updated_at = now(), source_import_id = $2::uuid
			WHERE m.area_id = $1
			  AND m.active
			  AND NOT EXISTS (SELECT 1 FROM inventory_stage s WHERE s.meter_no = m.meter_no)
		`, areaID, result.ImportID); err != nil {
			return fmt.Errorf("deactivate missing meters: %w", err)
		}
		return nil
	})
	if err != nil {
		return InventoryApplyResult{}, err
	}
	/* 盘点会改 meters 的 active 与楼栋楼层归属。
	   daily_campus_rollup 固化了旧名册。不重建则除数错误。
	   盘点低频。此处同步重建。 */
	if err := RefreshCampusRollup(ctx, pool); err != nil {
		return InventoryApplyResult{}, fmt.Errorf("rebuild campus rollup after inventory apply: %w", err)
	}
	return result, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
