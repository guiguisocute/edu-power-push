package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/auth"
	"github.com/edu-power-push/edu-power-push/backend/internal/biller"
	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/detailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/importer"
	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	// 注册内置上游。接入自有爬虫时替换此包。
	_ "github.com/edu-power-push/edu-power-push/backend/internal/provider/bdfairy"
	"github.com/edu-power-push/edu-power-push/backend/internal/scanner"
	"github.com/edu-power-push/edu-power-push/backend/internal/school"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
openUpstream 按当前生效学校打开查询器。

	命令行与常驻服务必须读同一配置。
	面板已换学校时，补采必须用新学校。
	pool 为 nil 时仅读 .env。
*/
func openUpstream(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) (provider.Querier, error) {
	binding := school.FromConfig(cfg)
	if pool != nil {
		if box, err := secrets.New(cfg.HTTP.SettingsKey); err == nil {
			if record, err := school.Load(ctx, pool, box, binding); err == nil {
				binding = record.Binding
			}
		}
	}
	querier, _, err := school.Resolve(binding, cfg.Scan.Timeout)
	if err != nil {
		return nil, err
	}
	if querier == nil {
		return nil, fmt.Errorf("%w: 先在运维面板选一所学校，或在 .env 里填 AREA_ID", provider.ErrSchoolNotConfigured)
	}
	return querier, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "query-meter":
		err = queryMeter(os.Args[2:])
	case "migrate":
		err = migrate()
	case "reset-admin":
		err = resetAdmin(os.Args[2:])
	case "import-inventory":
		err = importInventory(os.Args[2:])
	case "import-snapshot":
		err = importSnapshot(os.Args[2:])
	case "import-bills":
		err = importBills(os.Args[2:])
	case "scan":
		err = scan(os.Args[2:])
	case "refresh-rollups":
		err = refreshRollups(os.Args[2:])
	case "bill-scan":
		err = billScan(os.Args[2:])
	case "detail-scan":
		err = detailScan(os.Args[2:])
	case "import-details":
		err = importDetails(os.Args[2:])
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		slog.Error("admin command failed", "error", err)
		os.Exit(1)
	}
}

func resetAdmin(args []string) error {
	flags := flag.NewFlagSet("reset-admin", flag.ContinueOnError)
	email := flags.String("email", "", "new administrator email (defaults to the current administrator email)")
	nickname := flags.String("nickname", "管理员", "new administrator nickname")
	confirm := flags.Bool("confirm-replace-admins", false, "confirm deletion of every existing administrator account")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*confirm {
		return errors.New("--confirm-replace-admins is required")
	}
	passwordBytes, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
	if err != nil {
		return fmt.Errorf("read password from stdin: %w", err)
	}
	password := strings.TrimSpace(string(passwordBytes))
	if len(password) < 16 {
		return errors.New("password from stdin must contain at least 16 characters")
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reset transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck -- 成功路径由下方 commit 负责

	newEmail := strings.ToLower(strings.TrimSpace(*email))
	if newEmail == "" {
		err = tx.QueryRow(ctx, `
			SELECT email FROM user_accounts
			WHERE role='admin'
			ORDER BY created_at, id
			LIMIT 1
		`).Scan(&newEmail)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("--email is required when no administrator exists")
		}
		if err != nil {
			return fmt.Errorf("read current administrator email: %w", err)
		}
	}
	var replaced int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_accounts WHERE role='admin'`).Scan(&replaced); err != nil {
		return fmt.Errorf("count administrators: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_accounts WHERE role='admin'`); err != nil {
		return fmt.Errorf("delete administrators: %w", err)
	}
	userID := auth.NewID()
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_accounts
			(id,email,password_hash,nickname,status,email_verified_at,role)
		VALUES ($1::uuid,$2,$3,$4,'active',now(),'admin')
	`, userID, newEmail, passwordHash, strings.TrimSpace(*nickname)); err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admin_audit_log (actor_id,actor_label,action,target,detail)
		VALUES ($1::uuid,$2,'admin.reset','user_accounts',jsonb_build_object('replaced',$3::bigint))
	`, userID, newEmail, replaced); err != nil {
		return fmt.Errorf("record administrator reset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit administrator reset: %w", err)
	}
	return encodeJSON(map[string]any{"id": userID, "email": newEmail, "replaced": replaced})
}

func queryMeter(args []string) error {
	flags := flag.NewFlagSet("query-meter", flag.ContinueOnError)
	meter := flags.String("meter", "", "meter number to query")
	includeBill := flags.Bool("include-bill", false, "also fetch the current monthly bill")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*meter) == "" {
		return fmt.Errorf("--meter is required")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 本子命令不连库。仅用 .env 学校配置做试探查询。
	client, err := openUpstream(ctx, cfg, nil)
	if err != nil {
		return err
	}

	result := client.QueryMeter(ctx, *meter, provider.QueryOptions{
		IncludeBill: *includeBill,
		MaxAttempts: 1,
	})
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("query failed with status %s", result.Status)
	}
	return nil
}

func migrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	slog.Info("database migrations applied")
	return nil
}

func importInventory(args []string) error {
	flags := flag.NewFlagSet("import-inventory", flag.ContinueOnError)
	path := flags.String("file", "", "path to room_meters.json")
	campus := flags.String("campus", "示例校区", "campus display name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("--file is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	plan, err := importer.ValidateInventory(
		file,
		filepath.Base(*path),
		cfg.Upstream.AreaID,
		*campus,
		cfg.App.Timezone,
		importer.DefaultExcludedBuildings(),
	)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	result, err := storage.ApplyInventory(ctx, pool, plan)
	if err != nil {
		return err
	}
	return encodeJSON(result)
}

func importSnapshot(args []string) error {
	flags := flag.NewFlagSet("import-snapshot", flag.ContinueOnError)
	path := flags.String("file", "", "path to meter_balances.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("--file is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	plan, err := importer.ValidateSnapshot(
		file,
		filepath.Base(*path),
		cfg.App.Timezone,
		cfg.Scan.StaleAfter,
	)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	result, err := storage.ApplyLegacySnapshot(ctx, pool, plan)
	if err != nil {
		return err
	}
	return encodeJSON(result)
}

// importBills 从导出 CSV 补写往期月账单，用于灾难恢复。
// 关联使用 meter_no。忽略 CSV 中的旧库 meter_id。见 importer/bills.go。
func importBills(args []string) error {
	flags := flag.NewFlagSet("import-bills", flag.ContinueOnError)
	path := flags.String("file", "", "path to exported monthly_bills CSV")
	dryRun := flags.Bool("dry-run", false, "parse, join and upsert inside a transaction, then roll back")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("--file is required")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	// observed_at 列为 CST 墙上时间。必须用部署时区解析，否则偏移 8 小时。
	plan, err := importer.ValidateBills(file, filepath.Base(*path), cfg.App.Timezone)
	if err != nil {
		return err
	}
	for _, msg := range plan.Errors {
		slog.Warn("bill import row skipped", "detail", msg)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	result, err := storage.ApplyBillImport(ctx, pool, plan, *dryRun)
	if err != nil {
		return err
	}
	return encodeJSON(result)
}

func encodeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type scanOutput struct {
	RunID    string              `json:"run_id"`
	Counters storage.RunCounters `json:"counters"`
}

func scan(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	limit := flags.Int("limit", 0, "maximum meters to scan; required unless --full is set")
	full := flags.Bool("full", false, "explicitly scan the complete eligible inventory")
	bound := flags.Bool("bound", false, "scan only meters currently bound to active users")
	building := flags.String("building", "", "optional exact building scope")
	floor := flags.String("floor", "", "optional exact floor scope")
	qps := flags.Float64("qps", cfg.Scan.QPS, "balance meter attempts per second; HTTP requests also share the global gate")
	concurrency := flags.Int("concurrency", cfg.Scan.Concurrency, "worker count")
	resume := flags.String("resume", "", "resume an interrupted scan run ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *resume == "" {
		if *bound && (*full || *limit > 0 || strings.TrimSpace(*building) != "" || strings.TrimSpace(*floor) != "") {
			return fmt.Errorf("--bound cannot be combined with --full, --limit, --building, or --floor")
		}
		if *full && *limit > 0 {
			return fmt.Errorf("--full and --limit cannot be combined")
		}
		if !*full && !*bound && *limit < 1 {
			return fmt.Errorf("use a positive --limit for controlled scans, or explicitly pass --full or --bound")
		}
	}
	if *qps <= 0 || *concurrency < 1 {
		return fmt.Errorf("--qps and --concurrency must be positive")
	}
	if *qps > cfg.Scan.MaxQPS || *concurrency > cfg.Scan.MaxConcurrency {
		return fmt.Errorf("scan exceeds safety ceiling: qps <= %g and concurrency <= %d", cfg.Scan.MaxQPS, cfg.Scan.MaxConcurrency)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	requestGate, err := storage.NewUpstreamRequestGate(pool, cfg.Upstream.GlobalQPS, cfg.Upstream.GlobalConcurrency)
	if err != nil {
		return err
	}

	runID := strings.TrimSpace(*resume)
	if runID == "" {
		runID, err = storage.CreateScanRun(
			ctx,
			pool,
			"manual",
			nil,
			storage.ScanScope{
				Building:  strings.TrimSpace(*building),
				Floor:     strings.TrimSpace(*floor),
				BoundOnly: *bound,
				Limit:     *limit,
			},
			storage.ScanSettings{
				QPS:            *qps,
				Concurrency:    *concurrency,
				RetryMax:       cfg.Scan.RetryMax,
				TimeoutSeconds: int(cfg.Scan.Timeout.Seconds()),
			},
		)
		if err != nil {
			return err
		}
	}

	client, err := openUpstream(ctx, cfg, pool)
	if err != nil {
		return err
	}
	runner, err := scanner.New(
		client,
		scanner.PostgreSQLRepository{Pool: pool},
		scanner.Config{
			Concurrency:    *concurrency,
			QPS:            *qps,
			AcquireRequest: requestGate.Acquire,
			RetryMax:       cfg.Scan.RetryMax,
			BaseDelay:      cfg.Scan.RetryBaseDelay,
			MaxDelay:       cfg.Scan.RetryMaxDelay,
			StaleAfter:     cfg.Scan.StaleAfter,
			Location:       cfg.App.Timezone,
			ProgressEvery:  50,
		},
		slog.Default(),
	)
	if err != nil {
		return err
	}
	counters, err := runner.Run(ctx, runID)
	if err != nil {
		return err
	}
	if err := storage.RefreshRollupsForRun(ctx, pool, runID); err != nil {
		return fmt.Errorf("refresh scan rollups: %w", err)
	}
	return encodeJSON(scanOutput{RunID: runID, Counters: counters})
}

func refreshRollups(args []string) error {
	flags := flag.NewFlagSet("refresh-rollups", flag.ContinueOnError)
	fromValue := flags.String("from", "", "RFC3339 lower bound; defaults to the earliest consumption delta")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	var from time.Time
	if raw := strings.TrimSpace(*fromValue); raw != "" {
		from, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return fmt.Errorf("--from must be an RFC3339 timestamp: %w", err)
		}
	} else {
		var earliest *time.Time
		if err := pool.QueryRow(ctx, `SELECT min(usage_date)::timestamptz FROM effective_daily_consumption`).Scan(&earliest); err != nil {
			return err
		}
		if earliest == nil {
			return errors.New("no consumption deltas are available for rollup refresh")
		}
		from = *earliest
	}
	if err := storage.RefreshRollups(ctx, pool, from); err != nil {
		return err
	}
	// 全校空房除数在另一张物化视图中。
	// 手动刷新时一并重建，避免再跑第二条命令。
	if err := storage.RefreshCampusRollup(ctx, pool); err != nil {
		return err
	}
	return encodeJSON(map[string]any{"status": "completed", "from": from.UTC()})
}

type billScanOutput struct {
	RunID    string                  `json:"run_id"`
	Months   []string                `json:"months"`
	Counters storage.BillRunCounters `json:"counters"`
}

type detailScanOutput struct {
	RunID    string                         `json:"run_id"`
	Months   []string                       `json:"months"`
	Counters storage.DailyDetailRunCounters `json:"counters"`
}

type importDetailsOutput struct {
	DryRun bool                             `json:"dry_run"`
	Plan   importer.DailyDetailPlan         `json:"plan"`
	Import *storage.DailyDetailImportResult `json:"import,omitempty"`
}

func importDetails(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("import-details", flag.ContinueOnError)
	path := flags.String("file", "", "electricdetail JSONL export")
	summaryPath := flags.String("summary", "", "optional run_summary.json for provenance checks")
	logPath := flags.String("log", "", "optional full_run.log for completion checks")
	dryRun := flags.Bool("dry-run", false, "validate and report without database writes")
	acceptPartial := flags.Bool("accept-partial", false, "accept explicit failed cells or unmatched meters as audit gaps")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*path) == "" {
		return errors.New("--file is required")
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	plan, err := importer.ValidateDailyDetails(file, filepath.Base(*path), cfg.App.Timezone)
	if err != nil {
		_ = encodeJSON(importDetailsOutput{DryRun: *dryRun, Plan: plan})
		return err
	}
	provenance := map[string]string{}
	if strings.TrimSpace(*summaryPath) != "" {
		hash, err := validateDailyDetailSummary(*summaryPath, &plan)
		if err != nil {
			return err
		}
		provenance["summary_file"] = filepath.Base(*summaryPath)
		provenance["summary_sha256"] = hash
	}
	if strings.TrimSpace(*logPath) != "" {
		hash, err := validateDailyDetailLog(*logPath, plan.DeclaredMeters)
		if err != nil {
			return err
		}
		provenance["log_file"] = filepath.Base(*logPath)
		provenance["log_sha256"] = hash
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if *dryRun {
		if err := importer.StreamDailyDetailPlan(file, &plan, cfg.App.Timezone, func(importer.DailyDetailRecord) error { return nil }); err != nil {
			return err
		}
		return encodeJSON(importDetailsOutput{DryRun: true, Plan: plan})
	}
	if plan.UnresolvedFailedCells > 0 && !*acceptPartial {
		_ = encodeJSON(importDetailsOutput{Plan: plan})
		return fmt.Errorf("source retains %d unresolved month cells; review the dry-run and pass --accept-partial to import the valid cells", plan.UnresolvedFailedCells)
	}
	pool, err := storage.Open(context.Background(), cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	result, err := storage.ApplyDailyDetailImport(ctx, pool, file, &plan, cfg.App.Timezone, *acceptPartial, provenance)
	if err != nil {
		return err
	}
	return encodeJSON(importDetailsOutput{Plan: plan, Import: &result})
}

type dailyDetailRunSummary struct {
	MeterCount     int      `json:"meter_count"`
	Months         []string `json:"months"`
	FailMonthCells int      `json:"fail_month_cells"`
}

func validateDailyDetailSummary(path string, plan *importer.DailyDetailPlan) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var summary dailyDetailRunSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return "", fmt.Errorf("run summary is invalid JSON: %w", err)
	}
	if plan == nil {
		return "", errors.New("daily detail audit plan is required")
	}
	if summary.MeterCount < plan.Meters {
		return "", fmt.Errorf("run summary meter_count=%d is smaller than JSONL unique meters=%d", summary.MeterCount, plan.Meters)
	}
	if strings.Join(summary.Months, ",") != strings.Join(plan.Months, ",") {
		return "", errors.New("run summary month range does not match JSONL")
	}
	if summary.FailMonthCells < plan.UniqueFailedCells {
		return "", fmt.Errorf("run summary fail_month_cells=%d is smaller than JSONL retained failures=%d", summary.FailMonthCells, plan.UniqueFailedCells)
	}
	plan.DeclaredMeters = summary.MeterCount
	plan.DeclaredFailedCells = summary.FailMonthCells
	plan.MissingMeterCells = summary.FailMonthCells - plan.UniqueFailedCells - plan.SupersededFailedLines
	if plan.MissingMeterCells < 0 {
		return "", errors.New("run summary failure count is inconsistent with JSONL failures")
	}
	plan.UnresolvedFailedCells = plan.UniqueFailedCells + plan.MissingMeterCells
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func validateDailyDetailLog(path string, meters int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	completion := fmt.Sprintf("[%d/%d]", meters, meters)
	if !strings.Contains(string(data), completion) || !strings.Contains(string(data), "electricdetail.jsonl") {
		return "", errors.New("full run log has no final completion marker or JSONL output marker")
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:]), nil
}

func detailScan(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("detail-scan", flag.ContinueOnError)
	limit := flags.Int("limit", 0, "maximum meters; required unless --full is set")
	full := flags.Bool("full", false, "explicitly collect every eligible meter")
	bootstrap := flags.Bool("bootstrap", false, "force the configured historical bootstrap range; requires --full")
	building := flags.String("building", "", "optional exact building scope")
	floor := flags.String("floor", "", "optional exact floor scope")
	meter := flags.String("meter", "", "optional exact meter number")
	monthsValue := flags.String("months", "", "optional comma-separated YYYY-MM list; defaults to bootstrap or incremental mode")
	qps := flags.Float64("qps", cfg.Detail.QPS, "global upstream HTTP requests per second")
	concurrency := flags.Int("concurrency", cfg.Detail.Concurrency, "meter worker count")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *full && (*limit > 0 || strings.TrimSpace(*meter) != "") {
		return errors.New("--full cannot be combined with --limit or --meter")
	}
	if *bootstrap && !*full {
		return errors.New("--bootstrap requires --full")
	}
	if *bootstrap && strings.TrimSpace(*monthsValue) != "" {
		return errors.New("--bootstrap cannot be combined with --months")
	}
	if !*full && *limit < 1 && strings.TrimSpace(*meter) == "" {
		return errors.New("use a positive --limit for a staged probe, or explicitly pass --full")
	}
	if *qps <= 0 || *concurrency < 1 || *qps > cfg.Detail.MaxQPS || *concurrency > cfg.Detail.MaxConcurrency {
		return fmt.Errorf("detail scan safety limits: 0 < qps <= %g and 1 <= concurrency <= %d", cfg.Detail.MaxQPS, cfg.Detail.MaxConcurrency)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	requestGate, err := storage.NewUpstreamRequestGate(pool, cfg.Upstream.GlobalQPS, cfg.Upstream.GlobalConcurrency)
	if err != nil {
		return err
	}
	initialized, err := storage.DailyDetailsInitialized(ctx, pool)
	if err != nil {
		return err
	}
	months := []string{}
	for _, month := range strings.Split(*monthsValue, ",") {
		if month = strings.TrimSpace(month); month != "" {
			months = append(months, month)
		}
	}
	initialization := false
	if *bootstrap {
		initialization = strings.TrimSpace(*building) == "" && strings.TrimSpace(*floor) == ""
		months, err = detailer.BootstrapMonths(time.Now(), cfg.App.Timezone, cfg.Detail.BootstrapFrom)
		if err != nil {
			return err
		}
	} else if len(months) == 0 {
		if initialized {
			months = detailer.IncrementalMonths(time.Now(), cfg.App.Timezone)
		} else {
			// 分阶段探测可用 bootstrap 月份范围。
			// 禁止将分阶段探测标为全校已初始化。
			initialization = *full && strings.TrimSpace(*building) == "" && strings.TrimSpace(*floor) == ""
			months, err = detailer.BootstrapMonths(time.Now(), cfg.App.Timezone, cfg.Detail.BootstrapFrom)
			if err != nil {
				return err
			}
		}
	}
	runID, err := storage.CreateDailyDetailRun(ctx, pool, "manual", nil, months, storage.DailyDetailRunScope{
		Building: strings.TrimSpace(*building), Floor: strings.TrimSpace(*floor),
		Meter: strings.TrimSpace(*meter), Limit: *limit,
	}, storage.DailyDetailRunSettings{
		QPS: *qps, Concurrency: *concurrency, RetryMax: cfg.Detail.RetryMax,
		MonthRetryMax: cfg.Detail.MonthRetryMax, TimeoutSeconds: int(cfg.Scan.Timeout.Seconds()),
		BootstrapFrom: cfg.Detail.BootstrapFrom, Initialization: initialization,
	})
	if err != nil {
		return err
	}
	client, err := openUpstream(ctx, cfg, pool)
	if err != nil {
		return err
	}
	runner, err := detailer.New(client, detailer.PostgreSQLRepository{Pool: pool}, detailer.Config{
		Concurrency: *concurrency, QPS: *qps, AcquireRequest: requestGate.Acquire, RetryMax: cfg.Detail.RetryMax,
		MonthRetryMax: cfg.Detail.MonthRetryMax, ProgressEvery: 25, Location: cfg.App.Timezone,
	}, slog.Default())
	if err != nil {
		return err
	}
	counters, err := runner.Run(ctx, runID)
	if err != nil {
		return err
	}
	if err := storage.RefreshCampusRollup(ctx, pool); err != nil {
		return err
	}
	if err := storage.RefreshRollupsForDailyDetailRun(ctx, pool, runID); err != nil {
		return err
	}
	return encodeJSON(detailScanOutput{RunID: runID, Months: months, Counters: counters})
}

func billScan(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("bill-scan", flag.ContinueOnError)
	limit := flags.Int("limit", 0, "maximum meters; required unless --full is set")
	full := flags.Bool("full", false, "explicitly collect every eligible meter")
	building := flags.String("building", "", "optional exact building scope")
	floor := flags.String("floor", "", "optional exact floor scope")
	qps := flags.Float64("qps", cfg.Bill.QPS, "global upstream HTTP requests per second")
	concurrency := flags.Int("concurrency", cfg.Bill.Concurrency, "meter worker count")
	resume := flags.String("resume", "", "resume an interrupted bill run ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *resume == "" {
		if *full && *limit > 0 {
			return errors.New("--full and --limit cannot be combined")
		}
		if !*full && *limit < 1 {
			return errors.New("use a positive --limit for a staged probe, or explicitly pass --full")
		}
	}
	if *qps <= 0 || *concurrency < 1 || *qps > cfg.Bill.MaxQPS || *concurrency > cfg.Bill.MaxConcurrency {
		return fmt.Errorf("bill scan safety limits: 0 < qps <= %g and 1 <= concurrency <= %d", cfg.Bill.MaxQPS, cfg.Bill.MaxConcurrency)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		return err
	}
	requestGate, err := storage.NewUpstreamRequestGate(pool, cfg.Upstream.GlobalQPS, cfg.Upstream.GlobalConcurrency)
	if err != nil {
		return err
	}
	months := biller.VisibleMonths(time.Now(), cfg.App.Timezone)
	runID := strings.TrimSpace(*resume)
	if runID == "" {
		runID, err = storage.CreateBillRun(ctx, pool, "manual", nil, months, storage.BillRunScope{
			Building: strings.TrimSpace(*building), Floor: strings.TrimSpace(*floor), Limit: *limit,
		}, storage.BillRunSettings{
			QPS: *qps, Concurrency: *concurrency, RetryMax: cfg.Bill.RetryMax,
			MonthRetryMax: cfg.Bill.MonthRetryMax, TimeoutSeconds: int(cfg.Scan.Timeout.Seconds()),
		})
		if err != nil {
			return err
		}
	} else {
		item, err := storage.GetBillRun(ctx, pool, runID)
		if err != nil {
			return err
		}
		months = item.Months
		if item.QPS > 0 {
			*qps = item.QPS
		}
		if item.Concurrency > 0 {
			*concurrency = item.Concurrency
		}
	}
	client, err := openUpstream(ctx, cfg, pool)
	if err != nil {
		return err
	}
	runner, err := biller.New(client, biller.PostgreSQLRepository{Pool: pool}, biller.Config{
		Concurrency: *concurrency, QPS: *qps, AcquireRequest: requestGate.Acquire, RetryMax: cfg.Bill.RetryMax,
		MonthRetryMax: cfg.Bill.MonthRetryMax, ProgressEvery: 25,
	}, slog.Default())
	if err != nil {
		return err
	}
	counters, err := runner.Run(ctx, runID)
	if err != nil {
		return err
	}
	return encodeJSON(billScanOutput{RunID: runID, Months: months, Counters: counters})
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  admin query-meter --meter <number> [--include-bill]")
	fmt.Fprintln(os.Stderr, "  admin migrate")
	fmt.Fprintln(os.Stderr, "  admin reset-admin [--email <address>] [--nickname <name>] --confirm-replace-admins  # password via stdin")
	fmt.Fprintln(os.Stderr, "  admin import-inventory --file <room_meters.json>")
	fmt.Fprintln(os.Stderr, "  admin import-snapshot --file <meter_balances.json>")
	fmt.Fprintln(os.Stderr, "  admin import-bills --file <monthly_bills.csv> [--dry-run]")
	fmt.Fprintln(os.Stderr, "  admin scan --limit <n> [--qps <rate>] [--concurrency <n>]")
	fmt.Fprintln(os.Stderr, "  admin scan --full")
	fmt.Fprintln(os.Stderr, "  admin scan --resume <run-id>")
	fmt.Fprintln(os.Stderr, "  admin refresh-rollups [--from <RFC3339>]")
	fmt.Fprintln(os.Stderr, "  admin bill-scan --limit <n> [--qps <rate>] [--concurrency <n>]")
	fmt.Fprintln(os.Stderr, "  admin bill-scan --full")
	fmt.Fprintln(os.Stderr, "  admin bill-scan --resume <run-id>")
	fmt.Fprintln(os.Stderr, "  admin detail-scan --meter <number> [--months 2026-07]")
	fmt.Fprintln(os.Stderr, "  admin detail-scan --limit <n> [--months 2026-07] [--qps <rate>] [--concurrency <n>]")
	fmt.Fprintln(os.Stderr, "  admin detail-scan --full  # all meters, routine incremental months")
	fmt.Fprintln(os.Stderr, "  admin detail-scan --full --bootstrap  # disaster recovery: 2025-01..current")
	fmt.Fprintln(os.Stderr, "  admin import-details --file <electricdetail.jsonl> [--summary <run_summary.json>] [--log <full_run.log>] [--dry-run]")
}
