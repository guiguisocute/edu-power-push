package storage

/* 将导出的往期月账单写入 monthly_bills。
   使用 COPY 进临时表，再 INSERT…SELECT 关联。禁止逐行 upsert。
   关联键为 meter_no，不是 CSV 中的 meter_id。
   meter_id 为旧库主键。库存重导后 UUID 会变。见 importer/bills.go。 */

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
)

// BillImportResult 为一次导入结果。
// Matched 与 UnknownMeters 之和等于计划记录数。两数都必须上报。
type BillImportResult struct {
	Total         int      `json:"total"`          // CSV 解析成功行数
	Matched       int      `json:"matched"`        // 表号在库存中的行数
	Inserted      int      `json:"inserted"`       // 新写入
	Updated       int      `json:"updated"`        // 覆盖更旧记录
	Unchanged     int      `json:"unchanged"`      // 已有记录不更旧，原样保留
	UnknownMeters int      `json:"unknown_meters"` // 表号不在库存，未写入
	UnknownSample []string `json:"unknown_sample,omitempty"`
	Months        []string `json:"months"`
	Meters        int      `json:"meters"`
	Skipped       int      `json:"skipped"` // CSV 校验失败行
	DryRun        bool     `json:"dry_run"`
}

const unknownMeterSampleSize = 10

// ApplyBillImport 幂等导入月账单。
// dryRun 为真时完整执行后回滚。用于写库前检查结果。
func ApplyBillImport(
	ctx context.Context,
	pool *pgxpool.Pool,
	plan importer.BillPlan,
	dryRun bool,
) (BillImportResult, error) {
	if len(plan.Records) == 0 {
		return BillImportResult{}, errors.New("bill plan has no records")
	}

	result := BillImportResult{
		Total:   len(plan.Records),
		Months:  plan.Months,
		Meters:  plan.Meters,
		Skipped: plan.Skipped,
		DryRun:  dryRun,
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return BillImportResult{}, err
	}
	// dry-run 靠回滚实现。用于验证关联与约束。
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE bill_import_staging (
			meter_no text NOT NULL,
			month date NOT NULL,
			start_kwh numeric(20,4),
			end_kwh numeric(20,4),
			usage_kwh numeric(20,4),
			cost_yuan numeric(18,4),
			observed_at timestamptz NOT NULL,
			source text NOT NULL
		) ON COMMIT DROP`); err != nil {
		return BillImportResult{}, fmt.Errorf("create staging: %w", err)
	}

	rows := make([][]any, 0, len(plan.Records))
	for _, r := range plan.Records {
		rows = append(rows, []any{
			r.MeterNo, r.Month, r.StartKWh, r.EndKWh, r.UsageKWh, r.CostYuan, r.ObservedAt, r.Source,
		})
	}
	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"bill_import_staging"},
		[]string{"meter_no", "month", "start_kwh", "end_kwh", "usage_kwh", "cost_yuan", "observed_at", "source"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return BillImportResult{}, fmt.Errorf("copy staging: %w", err)
	}

	// 未知表号必须上报。不是错误。禁止静默丢数据。
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM bill_import_staging s
		WHERE NOT EXISTS (SELECT 1 FROM meters m WHERE m.meter_no = s.meter_no)
	`).Scan(&result.UnknownMeters); err != nil {
		return BillImportResult{}, fmt.Errorf("count unknown meters: %w", err)
	}
	if result.UnknownMeters > 0 {
		sample, err := tx.Query(ctx, `
			SELECT DISTINCT s.meter_no FROM bill_import_staging s
			WHERE NOT EXISTS (SELECT 1 FROM meters m WHERE m.meter_no = s.meter_no)
			ORDER BY s.meter_no LIMIT $1`, unknownMeterSampleSize)
		if err != nil {
			return BillImportResult{}, err
		}
		result.UnknownSample, err = pgx.CollectRows(sample, pgx.RowTo[string])
		if err != nil {
			return BillImportResult{}, err
		}
	}
	result.Matched = result.Total - result.UnknownMeters

	/* 冲突时仅覆盖更旧记录。导入补历史，禁止回滚现状。
	   xmax=0 区分插入与 DO UPDATE。 */
	insertSQL := `
		WITH src AS (
			SELECT m.id AS meter_id, s.month, s.start_kwh, s.end_kwh, s.usage_kwh,
			       s.cost_yuan, s.observed_at, s.source
			FROM bill_import_staging s
			JOIN meters m ON m.meter_no = s.meter_no
		), upserted AS (
			INSERT INTO monthly_bills
				(meter_id, month, start_kwh, end_kwh, usage_kwh, cost_yuan, observed_at, source)
			SELECT meter_id, month, start_kwh, end_kwh, usage_kwh, cost_yuan, observed_at, source
			FROM src
			ON CONFLICT (meter_id, month) DO UPDATE SET
				start_kwh  = EXCLUDED.start_kwh,
				end_kwh    = EXCLUDED.end_kwh,
				usage_kwh  = EXCLUDED.usage_kwh,
				cost_yuan  = EXCLUDED.cost_yuan,
				observed_at = EXCLUDED.observed_at,
				source     = EXCLUDED.source
			WHERE monthly_bills.observed_at < EXCLUDED.observed_at
			RETURNING (xmax = 0) AS inserted
		)
		SELECT
			count(*) FILTER (WHERE inserted)     AS inserted,
			count(*) FILTER (WHERE NOT inserted) AS updated
		FROM upserted`

	if err := tx.QueryRow(ctx, insertSQL).Scan(&result.Inserted, &result.Updated); err != nil {
		return BillImportResult{}, fmt.Errorf("upsert monthly_bills: %w", err)
	}
	// 已有记录不更旧。按原样保留。
	result.Unchanged = result.Matched - result.Inserted - result.Updated

	if dryRun {
		return result, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return BillImportResult{}, fmt.Errorf("commit: %w", err)
	}
	return result, nil
}
