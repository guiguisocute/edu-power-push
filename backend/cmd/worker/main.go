package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/biller"
	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/detailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/mailer"
	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	// 注册内置上游。接入自有爬虫时替换此包。见 docs/PROVIDERS.md。
	_ "github.com/edu-power-push/edu-power-push/backend/internal/provider/bdfairy"
	"github.com/edu-power-push/edu-power-push/backend/internal/push"
	"github.com/edu-power-push/edu-power-push/backend/internal/scanner"
	"github.com/edu-power-push/edu-power-push/backend/internal/school"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"
)

const workerName = "campus-scanner"

type worker struct {
	cfg           config.Config
	repo          scanner.PostgreSQLRepository
	client        *provider.Dynamic
	mailer        *mailer.Service
	settingsBox   *secrets.Box
	push          *push.Engine
	logger        *slog.Logger
	instance      string
	mu            sync.Mutex
	scanRunning   bool
	billRunning   bool
	detailRunning bool
	stopping      bool
	requestGate   *storage.UpstreamRequestGate
	// 合并余额 cron 触发。另一轮在跑时只记一次待跑。
	// 结束后由 5 秒轮询启动一轮。不丢触发，不重放多次。
	fullDue        bool
	fullScheduling bool
	// 限制未选学校日志频率。避免 5 秒轮询重复输出。
	schoolIdleLoggedAt  time.Time
	boundDue            bool
	boundScheduling     bool
	scheduler           *cron.Cron
	scheduleEntries     []cron.EntryID
	controlSettings     storage.ScannerControlSettings
	controlVersion      int64
	detailSchedule      cron.Schedule
	detailRetrySchedule cron.Schedule
	scanWG              sync.WaitGroup
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal("load configuration", err)
	}
	if cfg.Database.URL == "" {
		fatal("configuration", errors.New("DATABASE_URL is required"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := storage.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.App.Timezone.String())
	if err != nil {
		fatal("open database", err)
	}
	defer pool.Close()
	if err := storage.Migrate(ctx, pool); err != nil {
		fatal("apply migrations", err)
	}
	requestGate, err := storage.NewUpstreamRequestGate(
		pool, cfg.Upstream.GlobalQPS, cfg.Upstream.GlobalConcurrency,
	)
	if err != nil {
		fatal("configure shared upstream request gate", err)
	}
	// 邮件服务动态加载。面板改凭证后 worker 必须用新密钥。
	settingsBox, err := secrets.New(cfg.HTTP.SettingsKey)
	if err != nil {
		fatal("configure settings encryption", err)
	}
	/* 学校绑定动态加载。
	   面板换学校后，下一轮扫描用新查询器。
	   已启动的一轮保持开始时的查询器。 */
	client := provider.NewDynamic()
	binder := school.NewBinder(pool, settingsBox, school.FromConfig(cfg), cfg.Scan.Timeout, client, slog.Default())
	if err := binder.Refresh(ctx); err != nil {
		slog.Warn("initial school binding failed; collectors stay idle until it is fixed", "error", err)
	}
	go binder.Watch(ctx, 15*time.Second)
	if err := storage.EncryptLegacyChannelCredentials(ctx, pool, settingsBox); err != nil {
		fatal("encrypt legacy channel credentials", err)
	}
	mailService := mailer.NewDynamic(cfg.Mail, pool, settingsBox)
	hostname, _ := os.Hostname()
	w := &worker{
		cfg: cfg, repo: scanner.PostgreSQLRepository{Pool: pool}, client: client,
		mailer: mailService, settingsBox: settingsBox,
		push:   push.New(pool, mailService, settingsBox, slog.Default()),
		logger: slog.Default(), instance: fmt.Sprintf("%s-%d", hostname, os.Getpid()), requestGate: requestGate,
	}

	cronLogger := cron.VerbosePrintfLogger(log.New(os.Stderr, "cron: ", log.LstdFlags))
	scheduler := cron.New(cron.WithLocation(cfg.App.Timezone), cron.WithChain(cron.Recover(cronLogger), cron.SkipIfStillRunning(cronLogger)))
	w.scheduler = scheduler
	if _, err := scheduler.AddFunc("0 3 * * *", func() { w.maintenance(ctx) }); err != nil {
		fatal("schedule maintenance", err)
	}
	// 每分钟评估推送。定时摘要看 push_time 窗口。低额预警也在此触发。
	if _, err := scheduler.AddFunc("* * * * *", func() { w.runPush(ctx) }); err != nil {
		fatal("schedule push engine", err)
	}
	if err := w.reloadScannerSettings(ctx, true); err != nil {
		fatal("load scanner settings", err)
	}
	scheduler.Start()
	defer func() { <-scheduler.Stop().Done() }()

	heartbeatTicker := time.NewTicker(30 * time.Second)
	pollTicker := time.NewTicker(5 * time.Second)
	recoveryTicker := time.NewTicker(time.Minute)
	settingsTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()
	defer pollTicker.Stop()
	defer recoveryTicker.Stop()
	defer settingsTicker.Stop()
	w.heartbeat(ctx)
	w.maintenance(ctx)
	w.recover(ctx)
	w.runPending(ctx)
	w.catchUpDailyDetails(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			w.shutdown()
			return
		case <-heartbeatTicker.C:
			w.heartbeat(ctx)
		case <-pollTicker.C:
			w.runFullDue(ctx)
			w.runBoundDue(ctx)
			w.runPending(ctx)
		case <-recoveryTicker.C:
			w.recover(ctx)
			w.catchUpDailyDetails(ctx, time.Now())
		case <-settingsTicker.C:
			if err := w.reloadScannerSettings(ctx, false); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.Error("reload scanner settings", "error", err)
			}
		}
	}
}

func (w *worker) scannerSettingsFromEnv() storage.ScannerControlSettings {
	return storage.ScannerControlSettings{
		Shared: storage.SharedUpstreamSettings{
			QPS: w.cfg.Upstream.GlobalQPS, Concurrency: w.cfg.Upstream.GlobalConcurrency,
		},
		Balance: storage.BalanceScannerSettings{
			Cron: w.cfg.Scan.Cron, BoundCron: w.cfg.Scan.BoundCron, QPS: w.cfg.Scan.QPS,
			Concurrency: w.cfg.Scan.Concurrency, RetryMax: w.cfg.Scan.RetryMax,
			Enabled: w.cfg.Scan.Cron != "" || w.cfg.Scan.BoundCron != "",
		},
		Bills: storage.BatchScannerSettings{
			Cron: w.cfg.Bill.Cron, QPS: w.cfg.Bill.QPS, Concurrency: w.cfg.Bill.Concurrency,
			RetryMax: w.cfg.Bill.RetryMax, MonthRetryMax: w.cfg.Bill.MonthRetryMax,
			Enabled: w.cfg.Bill.Cron != "",
		},
		DailyDetails: storage.DetailScannerSettings{
			BatchScannerSettings: storage.BatchScannerSettings{
				Cron: w.cfg.Detail.Cron, QPS: w.cfg.Detail.QPS, Concurrency: w.cfg.Detail.Concurrency,
				RetryMax: w.cfg.Detail.RetryMax, MonthRetryMax: w.cfg.Detail.MonthRetryMax,
				Enabled: w.cfg.Detail.Cron != "",
			},
			RetryCron: w.cfg.Detail.RetryCron, BootstrapFrom: w.cfg.Detail.BootstrapFrom,
			AutoBootstrap: w.cfg.Detail.AutoBootstrap,
		},
	}
}

func (w *worker) scannerSettings() storage.ScannerControlSettings {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.controlSettings
}

// reloadScannerSettings 使面板改动立即生效，无需重启 worker。
// 已启动任务保持冻结的 config_snapshot。
// 仅后续任务与 cron 条目使用新配置。
func (w *worker) reloadScannerSettings(ctx context.Context, force bool) error {
	record, err := storage.LoadScannerControlSettings(
		ctx, w.repo.Pool, w.settingsBox, w.scannerSettingsFromEnv(),
	)
	if err != nil {
		return err
	}
	if err := w.validateScannerSafety(record.Settings); err != nil {
		return err
	}
	w.mu.Lock()
	if !force && record.Version == w.controlVersion {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()

	var detailSchedule, detailRetrySchedule cron.Schedule
	checks := []struct {
		enabled bool
		expr    string
		name    string
	}{
		{record.Settings.Balance.Enabled, record.Settings.Balance.Cron, "balance cron"},
		{record.Settings.Balance.Enabled, record.Settings.Balance.BoundCron, "bound balance cron"},
		{record.Settings.Bills.Enabled, record.Settings.Bills.Cron, "bill cron"},
		{record.Settings.DailyDetails.Enabled, record.Settings.DailyDetails.Cron, "daily detail cron"},
		{record.Settings.DailyDetails.Enabled, record.Settings.DailyDetails.RetryCron, "daily detail retry cron"},
	}
	for _, check := range checks {
		if !check.enabled || check.expr == "" {
			continue
		}
		parsed, err := cron.ParseStandard(check.expr)
		if err != nil {
			return fmt.Errorf("parse %s: %w", check.name, err)
		}
		if check.name == "daily detail cron" {
			detailSchedule = parsed
		} else if check.name == "daily detail retry cron" {
			detailRetrySchedule = parsed
		}
	}

	w.mu.Lock()
	for _, entry := range w.scheduleEntries {
		w.scheduler.Remove(entry)
	}
	w.scheduleEntries = nil
	add := func(enabled bool, expr string, fn func()) error {
		if !enabled || expr == "" {
			return nil
		}
		entry, err := w.scheduler.AddFunc(expr, fn)
		if err != nil {
			return err
		}
		w.scheduleEntries = append(w.scheduleEntries, entry)
		return nil
	}
	if err := add(record.Settings.Balance.Enabled, record.Settings.Balance.Cron, func() { w.markFullDue(ctx) }); err != nil {
		w.mu.Unlock()
		return err
	}
	if err := add(record.Settings.Balance.Enabled, record.Settings.Balance.BoundCron, func() { w.markBoundDue(ctx) }); err != nil {
		w.mu.Unlock()
		return err
	}
	if err := add(record.Settings.Bills.Enabled, record.Settings.Bills.Cron, func() { w.scheduleBills(ctx) }); err != nil {
		w.mu.Unlock()
		return err
	}
	if record.Settings.DailyDetails.Enabled && detailSchedule != nil {
		entry := w.scheduler.Schedule(detailSchedule, cron.FuncJob(func() { w.catchUpDailyDetails(ctx, time.Now()) }))
		w.scheduleEntries = append(w.scheduleEntries, entry)
	}
	if record.Settings.DailyDetails.Enabled && detailRetrySchedule != nil {
		entry := w.scheduler.Schedule(detailRetrySchedule, cron.FuncJob(func() { w.catchUpDailyDetails(ctx, time.Now()) }))
		w.scheduleEntries = append(w.scheduleEntries, entry)
	}
	w.controlSettings = record.Settings
	w.controlVersion = record.Version
	w.detailSchedule = detailSchedule
	w.detailRetrySchedule = detailRetrySchedule
	w.mu.Unlock()
	// 同步更新上游闸门。已发出的请求保持旧限额。
	if w.requestGate != nil {
		if err := w.requestGate.SetLimits(
			record.Settings.Shared.QPS, record.Settings.Shared.Concurrency,
		); err != nil {
			w.logger.Error("apply shared upstream gate", "error", err)
		}
	}
	w.logger.Info("scanner schedules applied", "source", record.Source, "version", record.Version,
		"shared_qps", record.Settings.Shared.QPS, "shared_concurrency", record.Settings.Shared.Concurrency)
	return nil
}

func (w *worker) validateScannerSafety(settings storage.ScannerControlSettings) error {
	if settings.Shared.QPS <= 0 || settings.Shared.QPS > w.cfg.Upstream.MaxQPS {
		return fmt.Errorf("shared gate qps %g exceeds runtime safety range (0,%g]",
			settings.Shared.QPS, w.cfg.Upstream.MaxQPS)
	}
	if settings.Shared.Concurrency < 1 || settings.Shared.Concurrency > w.cfg.Upstream.MaxConcurrency {
		return fmt.Errorf("shared gate concurrency %d exceeds runtime safety range [1,%d]",
			settings.Shared.Concurrency, w.cfg.Upstream.MaxConcurrency)
	}
	checks := []struct {
		name                        string
		qps, maxQPS                 float64
		concurrency, maxConcurrency int
		retryMax, monthRetryMax     int
	}{
		{"balance", settings.Balance.QPS, w.cfg.Scan.MaxQPS, settings.Balance.Concurrency, w.cfg.Scan.MaxConcurrency, settings.Balance.RetryMax, 0},
		{"bills", settings.Bills.QPS, w.cfg.Bill.MaxQPS, settings.Bills.Concurrency, w.cfg.Bill.MaxConcurrency, settings.Bills.RetryMax, settings.Bills.MonthRetryMax},
		{"daily details", settings.DailyDetails.QPS, w.cfg.Detail.MaxQPS, settings.DailyDetails.Concurrency, w.cfg.Detail.MaxConcurrency, settings.DailyDetails.RetryMax, settings.DailyDetails.MonthRetryMax},
	}
	for _, check := range checks {
		if check.qps <= 0 || check.qps > check.maxQPS {
			return fmt.Errorf("%s qps %g exceeds runtime safety range (0,%g]", check.name, check.qps, check.maxQPS)
		}
		if check.concurrency < 1 || check.concurrency > check.maxConcurrency {
			return fmt.Errorf("%s concurrency %d exceeds runtime safety range [1,%d]", check.name, check.concurrency, check.maxConcurrency)
		}
		if check.retryMax < 0 || check.monthRetryMax < 0 {
			return fmt.Errorf("%s retry settings must not be negative", check.name)
		}
	}
	return nil
}

// catchUpDailyDetails 保证日明细调度可恢复，不新增队列表。
// 今日配置时刻已过且库中无 schedule 记录时即补跑。
// worker 重启后仍有效。每轮 recovery 重试直至无阻塞。
func (w *worker) catchUpDailyDetails(ctx context.Context, now time.Time) {
	w.mu.Lock()
	schedule, retrySchedule := w.detailSchedule, w.detailRetrySchedule
	enabled := w.controlSettings.DailyDetails.Enabled
	w.mu.Unlock()
	if !enabled {
		return
	}
	if dailyDetailDueToday(now, w.cfg.App.Timezone, schedule) {
		w.scheduleDailyDetails(ctx)
	}
	if dailyDetailDueToday(now, w.cfg.App.Timezone, retrySchedule) {
		w.scheduleUnpublishedDailyDetails(ctx, now)
	}
}

func dailyDetailDueToday(now time.Time, location *time.Location, schedule cron.Schedule) bool {
	if location == nil || schedule == nil {
		return false
	}
	localNow := now.In(location)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	firstToday := schedule.Next(dayStart.Add(-time.Nanosecond))
	return firstToday.Before(dayStart.AddDate(0, 0, 1)) && !firstToday.After(localNow)
}

func (w *worker) heartbeat(ctx context.Context) {
	settings := w.scannerSettings()
	metadata, _ := json.Marshal(map[string]any{
		"pid": os.Getpid(), "scan_cron": settings.Balance.Cron, "bill_cron": settings.Bills.Cron,
		"bound_scan_cron": settings.Balance.BoundCron, "detail_cron": settings.DailyDetails.Cron,
		"detail_retry_cron": settings.DailyDetails.RetryCron,
		// 上报闸门当前生效值，不用 .env 默认值。
		"upstream_global_qps":         settings.Shared.QPS,
		"upstream_global_concurrency": settings.Shared.Concurrency,
	})
	if err := storage.UpsertWorkerHeartbeat(ctx, w.repo.Pool, workerName, w.instance, metadata); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("worker heartbeat", "error", err)
	}
}

func (w *worker) recover(ctx context.Context) {
	if reaped, err := storage.FailStaleMeterRefreshes(ctx, w.repo.Pool, 3*time.Minute); err != nil {
		if !errors.Is(err, context.Canceled) {
			w.logger.Error("reap stale meter refreshes", "error", err)
		}
	} else if reaped > 0 {
		w.logger.Warn("failed orphaned meter refreshes", "count", reaped)
	}
	w.recoverScan(ctx)
	w.recoverBill(ctx)
	w.recoverDailyDetails(ctx)
}

/*
resumable 判定中断 run 是否自动续跑。

	仅拦截 trigger 为 schedule 的任务。
	SCHEDULE 为 MANUAL 时禁止自动捡回。
	manual / retry / recovery 仍续跑；停止请取消。
*/
func (w *worker) resumable(kind, trigger string) bool {
	if trigger != "schedule" {
		return true
	}
	settings := w.scannerSettings()
	switch kind {
	case "bill":
		return settings.Bills.Enabled
	case "daily detail":
		return settings.DailyDetails.Enabled
	default:
		return settings.Balance.Enabled
	}
}

func (w *worker) recoverScan(ctx context.Context) {
	scan, err := storage.FindInterruptedScan(ctx, w.repo.Pool)
	if err == nil {
		if !w.resumable("balance", scan.Trigger) {
			return
		}
		w.logger.Warn("resuming interrupted scan", "run_id", scan.RunID)
		w.start(ctx, scan)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find interrupted scan", "error", err)
		return
	}

	scan, err = storage.InterruptStaleScan(ctx, w.repo.Pool, 3*time.Minute)
	if err == nil {
		if !w.resumable("balance", scan.Trigger) {
			return
		}
		w.logger.Warn("recovering stale scan", "run_id", scan.RunID)
		w.start(ctx, scan)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find stale scan", "error", err)
	}
}

func (w *worker) recoverBill(ctx context.Context) {
	bill, err := storage.FindInterruptedBill(ctx, w.repo.Pool)
	if err == nil {
		if !w.resumable("bill", bill.Trigger) {
			return
		}
		w.logger.Warn("resuming interrupted bill run", "run_id", bill.RunID)
		w.startBill(ctx, bill)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find interrupted bill run", "error", err)
		return
	}
	bill, err = storage.InterruptStaleBill(ctx, w.repo.Pool, 3*time.Minute)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		w.logger.Error("find stale bill run", "error", err)
		return
	}
	if !w.resumable("bill", bill.Trigger) {
		return
	}
	w.logger.Warn("recovering stale bill run", "run_id", bill.RunID)
	w.startBill(ctx, bill)
}

func (w *worker) recoverDailyDetails(ctx context.Context) {
	detail, err := storage.FindInterruptedDailyDetail(ctx, w.repo.Pool)
	if err == nil {
		if !w.resumable("daily detail", detail.Trigger) {
			return
		}
		w.logger.Warn("resuming interrupted daily detail run", "run_id", detail.RunID)
		w.startDailyDetails(ctx, detail)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find interrupted daily detail run", "error", err)
		return
	}
	detail, err = storage.InterruptStaleDailyDetail(ctx, w.repo.Pool, 3*time.Minute)
	if errors.Is(err, pgx.ErrNoRows) {
		return
	}
	if err != nil {
		w.logger.Error("find stale daily detail run", "error", err)
		return
	}
	if !w.resumable("daily detail", detail.Trigger) {
		return
	}
	w.logger.Warn("recovering stale daily detail run", "run_id", detail.RunID)
	w.startDailyDetails(ctx, detail)
}

/*
runPending 领取并启动待办采集任务。

	未选学校时跳过本轮，任务留在 pending。
	避免 school_not_configured 失败污染运行记录。
*/
func (w *worker) runPending(ctx context.Context) {
	if !w.client.Ready() {
		w.logSchoolIdle()
		return
	}
	scan, err := storage.FindPendingScan(ctx, w.repo.Pool)
	if err == nil {
		w.start(ctx, scan)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find pending scan", "error", err)
	}
	bill, err := storage.FindPendingBill(ctx, w.repo.Pool)
	if err == nil {
		w.startBill(ctx, bill)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find pending bill run", "error", err)
	}
	detail, err := storage.FindPendingDailyDetail(ctx, w.repo.Pool)
	if err == nil {
		w.startDailyDetails(ctx, detail)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		w.logger.Error("find pending daily detail run", "error", err)
	}
}

/*
logSchoolIdle 输出未选学校警告。每 10 分钟最多一次。

	runPending 每 5 秒一轮。高频日志会掩盖真实错误。
*/
func (w *worker) logSchoolIdle() {
	w.mu.Lock()
	quiet := time.Since(w.schoolIdleLoggedAt) < 10*time.Minute
	if !quiet {
		w.schoolIdleLoggedAt = time.Now()
	}
	w.mu.Unlock()
	if !quiet {
		w.logger.Warn("no school configured; skipping collector runs until one is selected in the admin panel")
	}
}

func (w *worker) markFullDue(ctx context.Context) {
	w.mu.Lock()
	w.fullDue = true
	w.mu.Unlock()
	w.runFullDue(ctx)
}

func (w *worker) runFullDue(ctx context.Context) {
	w.mu.Lock()
	if !w.fullDue || w.fullScheduling || w.scanRunning || w.stopping {
		w.mu.Unlock()
		return
	}
	w.fullScheduling = true
	w.mu.Unlock()

	handled := w.scheduleFull(ctx)
	w.mu.Lock()
	w.fullScheduling = false
	if handled {
		w.fullDue = false
	}
	w.mu.Unlock()
}

// scheduleFull 仅在另一轮余额任务占用时返回 false。
func (w *worker) scheduleFull(ctx context.Context) bool {
	configured := w.scannerSettings().Balance
	if !configured.Enabled {
		return true
	}
	runID, err := storage.CreateScanRun(ctx, w.repo.Pool, "schedule", nil, storage.ScanScope{}, storage.ScanSettings{
		QPS: configured.QPS, Concurrency: configured.Concurrency, RetryMax: configured.RetryMax,
		TimeoutSeconds: int(w.cfg.Scan.Timeout.Seconds()),
	})
	if errors.Is(err, storage.ErrScanAlreadyRunning) {
		w.logger.Warn("scheduled scan deferred because another balance run is active")
		return false
	}
	if err != nil {
		w.logger.Error("create scheduled scan", "error", err)
		w.notifyFailure("create scheduled scan: " + err.Error())
		return true
	}
	w.start(ctx, storage.RunnableScan{RunID: runID, QPS: configured.QPS, Concurrency: configured.Concurrency, RetryMax: configured.RetryMax})
	return true
}

func (w *worker) markBoundDue(ctx context.Context) {
	w.mu.Lock()
	w.boundDue = true
	w.mu.Unlock()
	w.runBoundDue(ctx)
}

func (w *worker) runBoundDue(ctx context.Context) {
	w.mu.Lock()
	if !w.boundDue || w.boundScheduling || w.scanRunning || w.stopping {
		w.mu.Unlock()
		return
	}
	w.boundScheduling = true
	w.mu.Unlock()

	handled := w.scheduleBound(ctx)
	w.mu.Lock()
	w.boundScheduling = false
	if handled {
		w.boundDue = false
	}
	w.mu.Unlock()
}

// scheduleBound 仅刷新已绑定活跃用户的电表。
// 仍走批量任务与共享上游限额。禁止与另一轮余额任务重叠。
// 仅在另一轮余额任务占用时返回 false。调用方保留该小时触发并稍后重试。
func (w *worker) scheduleBound(ctx context.Context) bool {
	configured := w.scannerSettings().Balance
	if !configured.Enabled {
		return true
	}
	runID, err := storage.CreateScanRun(ctx, w.repo.Pool, "schedule", nil, storage.ScanScope{BoundOnly: true}, storage.ScanSettings{
		QPS: configured.QPS, Concurrency: configured.Concurrency, RetryMax: configured.RetryMax,
		TimeoutSeconds: int(w.cfg.Scan.Timeout.Seconds()),
	})
	if errors.Is(err, storage.ErrScanScopeEmpty) {
		w.logger.Info("scheduled bound-meter scan skipped because no active user has a bound meter")
		return true
	}
	if errors.Is(err, storage.ErrScanAlreadyRunning) {
		w.logger.Info("scheduled bound-meter scan deferred because another balance run is active")
		return false
	}
	if err != nil {
		w.logger.Error("create scheduled bound-meter scan", "error", err)
		w.notifyFailure("create scheduled bound-meter scan: " + err.Error())
		return true
	}
	w.start(ctx, storage.RunnableScan{RunID: runID, QPS: configured.QPS, Concurrency: configured.Concurrency, RetryMax: configured.RetryMax})
	return true
}

func (w *worker) scheduleBills(ctx context.Context) {
	configured := w.scannerSettings().Bills
	if !configured.Enabled {
		return
	}
	months := biller.VisibleMonths(time.Now(), w.cfg.App.Timezone)
	runID, err := storage.CreateBillRun(ctx, w.repo.Pool, "schedule", nil, months, storage.BillRunScope{}, w.billSettings(configured))
	if errors.Is(err, storage.ErrBillScheduleExists) {
		w.logger.Info("scheduled bill reconciliation already exists for this month")
		return
	}
	if errors.Is(err, storage.ErrBillAlreadyRunning) {
		w.logger.Warn("scheduled bill reconciliation deferred because another bill run is active")
		return
	}
	if err != nil {
		w.logger.Error("create scheduled bill run", "error", err)
		w.notifyFailure("create scheduled bill run: " + err.Error())
		return
	}
	w.startBill(ctx, storage.RunnableBill{
		RunID: runID, QPS: configured.QPS, Concurrency: configured.Concurrency,
		RetryMax: configured.RetryMax, MonthRetryMax: configured.MonthRetryMax,
	})
}

func (w *worker) scheduleDailyDetails(ctx context.Context) {
	configured := w.scannerSettings().DailyDetails
	if !configured.Enabled {
		return
	}
	initialized, err := storage.DailyDetailsInitialized(ctx, w.repo.Pool)
	if err != nil {
		w.logger.Error("check daily detail initialization", "error", err)
		return
	}
	months := detailer.IncrementalMonths(time.Now(), w.cfg.App.Timezone)
	initialization := !initialized && configured.AutoBootstrap
	if initialization {
		months, err = detailer.BootstrapMonths(time.Now(), w.cfg.App.Timezone, configured.BootstrapFrom)
		if err != nil {
			w.logger.Error("build daily detail bootstrap range", "error", err)
			return
		}
	}
	runID, err := storage.CreateDailyDetailRun(ctx, w.repo.Pool, "schedule", nil, months,
		storage.DailyDetailRunScope{}, w.detailSettings(configured, initialization))
	if errors.Is(err, storage.ErrDailyDetailScheduleExists) {
		w.logger.Info("scheduled daily detail run already exists today")
		return
	}
	if errors.Is(err, storage.ErrDailyDetailAlreadyRunning) {
		w.logger.Warn("scheduled daily detail run deferred because another daily detail run is active; will retry within one minute")
		return
	}
	if err != nil {
		w.logger.Error("create scheduled daily detail run", "error", err)
		w.notifyFailure("create scheduled daily detail run: " + err.Error())
		return
	}
	w.startDailyDetails(ctx, storage.RunnableDailyDetail{
		RunID: runID, QPS: configured.QPS, Concurrency: configured.Concurrency,
		RetryMax: configured.RetryMax, MonthRetryMax: configured.MonthRetryMax,
	})
}

func (w *worker) scheduleUnpublishedDailyDetails(ctx context.Context, now time.Time) {
	configured := w.scannerSettings().DailyDetails
	if !configured.Enabled || configured.RetryCron == "" {
		return
	}
	targetDate := dailyDetailTargetDate(now, w.cfg.App.Timezone)
	months := detailer.IncrementalMonths(now, w.cfg.App.Timezone)
	runID, err := storage.CreateDailyDetailRun(ctx, w.repo.Pool, "retry", nil, months,
		storage.DailyDetailRunScope{UnpublishedDate: targetDate}, w.detailSettings(configured, false))
	if errors.Is(err, storage.ErrDailyDetailSettlementRetryExists) {
		w.logger.Info("daily detail settlement retry already exists", "target_date", targetDate)
		return
	}
	if errors.Is(err, storage.ErrDailyDetailAlreadyRunning) {
		w.logger.Warn("daily detail settlement retry deferred because another daily detail run is active", "target_date", targetDate)
		return
	}
	if err != nil {
		w.logger.Error("create daily detail settlement retry", "target_date", targetDate, "error", err)
		w.notifyFailure("create daily detail settlement retry: " + err.Error())
		return
	}
	w.startDailyDetails(ctx, storage.RunnableDailyDetail{
		RunID: runID, QPS: configured.QPS, Concurrency: configured.Concurrency,
		RetryMax: configured.RetryMax, MonthRetryMax: configured.MonthRetryMax,
	})
}

func dailyDetailTargetDate(now time.Time, location *time.Location) string {
	if location == nil {
		location = time.UTC
	}
	return now.In(location).AddDate(0, 0, -1).Format("2006-01-02")
}

func (w *worker) start(ctx context.Context, scan storage.RunnableScan) {
	w.mu.Lock()
	if w.scanRunning || w.stopping {
		w.mu.Unlock()
		return
	}
	w.scanRunning = true
	w.scanWG.Add(1)
	w.mu.Unlock()
	go func() {
		defer func() {
			w.mu.Lock()
			w.scanRunning = false
			w.mu.Unlock()
			w.scanWG.Done()
		}()
		configured := w.scannerSettings().Balance
		qps, concurrency, retryMax := scan.QPS, scan.Concurrency, scan.RetryMax
		if qps <= 0 {
			qps = configured.QPS
		}
		if concurrency < 1 {
			concurrency = configured.Concurrency
		}
		runner, err := scanner.New(w.client, w.repo, scanner.Config{
			Concurrency: concurrency, QPS: qps, AcquireRequest: w.requestGate.Acquire, RetryMax: retryMax,
			BaseDelay: w.cfg.Scan.RetryBaseDelay, MaxDelay: w.cfg.Scan.RetryMaxDelay,
			StaleAfter: w.cfg.Scan.StaleAfter, Location: w.cfg.App.Timezone, ProgressEvery: 50,
		}, w.logger)
		if err != nil {
			w.notifyFailure("create scan runner: " + err.Error())
			return
		}
		counters, err := runner.Run(ctx, scan.RunID)
		if err != nil {
			if errors.Is(err, storage.ErrScanNotRunnable) {
				w.logger.Info("scan was claimed by another process", "run_id", scan.RunID)
				return
			}
			if errors.Is(err, context.Canceled) {
				w.logger.Info("scan checkpointed during worker shutdown", "run_id", scan.RunID)
				return
			}
			w.logger.Error("worker scan failed", "run_id", scan.RunID, "error", err)
			w.notifyFailure("scan " + scan.RunID + " failed: " + err.Error())
			return
		}
		if err := storage.RefreshRollupsForRun(context.Background(), w.repo.Pool, scan.RunID); err != nil {
			w.logger.Error("refresh scan rollups", "run_id", scan.RunID, "error", err)
		}
		// 余额已写入。立即跑低额预警。
		w.runPush(context.Background())
		if w.mailer != nil && w.cfg.Mail.AdminTo != "" {
			if _, err := w.mailer.SendScanSummary(context.Background(), w.cfg.Mail.AdminTo, scan.RunID, "completed", counters); err != nil {
				w.logger.Error("send scan summary", "run_id", scan.RunID, "error", err)
			}
			critical, countErr := storage.CountOpenCriticalAnomaliesForRun(context.Background(), w.repo.Pool, scan.RunID)
			if countErr != nil {
				w.logger.Error("count critical scan anomalies", "run_id", scan.RunID, "error", countErr)
			} else if critical > 0 {
				if _, err := w.mailer.SendAnomalySummary(context.Background(), w.cfg.Mail.AdminTo, scan.RunID, critical); err != nil {
					w.logger.Error("send anomaly summary", "run_id", scan.RunID, "error", err)
				}
			}
		}
	}()
}

func (w *worker) startBill(ctx context.Context, bill storage.RunnableBill) {
	w.mu.Lock()
	if w.billRunning || w.stopping {
		w.mu.Unlock()
		return
	}
	w.billRunning = true
	w.scanWG.Add(1)
	w.mu.Unlock()
	go func() {
		defer func() {
			w.mu.Lock()
			w.billRunning = false
			w.mu.Unlock()
			w.scanWG.Done()
		}()
		configured := w.scannerSettings().Bills
		qps, concurrency := bill.QPS, bill.Concurrency
		if qps <= 0 {
			qps = configured.QPS
		}
		if concurrency < 1 {
			concurrency = configured.Concurrency
		}
		retryMax, monthRetryMax := bill.RetryMax, bill.MonthRetryMax
		if retryMax < 0 {
			retryMax = configured.RetryMax
		}
		if monthRetryMax < 0 {
			monthRetryMax = configured.MonthRetryMax
		}
		runner, err := biller.New(w.client, biller.PostgreSQLRepository{Pool: w.repo.Pool}, biller.Config{
			Concurrency: concurrency, QPS: qps, AcquireRequest: w.requestGate.Acquire,
			RetryMax: retryMax, MonthRetryMax: monthRetryMax, ProgressEvery: 25,
		}, w.logger)
		if err != nil {
			w.notifyFailure("create bill runner: " + err.Error())
			return
		}
		counters, err := runner.Run(ctx, bill.RunID)
		if err != nil {
			if errors.Is(err, storage.ErrBillNotRunnable) {
				w.logger.Info("bill run was claimed by another process", "run_id", bill.RunID)
				return
			}
			if errors.Is(err, context.Canceled) {
				w.logger.Info("bill run checkpointed during worker shutdown", "run_id", bill.RunID)
				return
			}
			w.logger.Error("worker bill run failed", "run_id", bill.RunID, "error", err)
			w.notifyFailure("bill run " + bill.RunID + " failed: " + err.Error())
			return
		}
		w.logger.Info("bill reconciliation completed", "run_id", bill.RunID, "counters", counters)
	}()
}

func (w *worker) startDailyDetails(ctx context.Context, detail storage.RunnableDailyDetail) {
	w.mu.Lock()
	if w.detailRunning || w.stopping {
		w.mu.Unlock()
		return
	}
	w.detailRunning = true
	w.scanWG.Add(1)
	w.mu.Unlock()
	go func() {
		defer func() {
			w.mu.Lock()
			w.detailRunning = false
			w.mu.Unlock()
			w.scanWG.Done()
		}()
		configured := w.scannerSettings().DailyDetails
		qps, concurrency := detail.QPS, detail.Concurrency
		if qps <= 0 {
			qps = configured.QPS
		}
		if concurrency < 1 {
			concurrency = configured.Concurrency
		}
		retryMax, monthRetryMax := detail.RetryMax, detail.MonthRetryMax
		if retryMax < 0 {
			retryMax = configured.RetryMax
		}
		if monthRetryMax < 0 {
			monthRetryMax = configured.MonthRetryMax
		}
		runner, err := detailer.New(w.client, detailer.PostgreSQLRepository{Pool: w.repo.Pool}, detailer.Config{
			Concurrency: concurrency, QPS: qps, AcquireRequest: w.requestGate.Acquire,
			RetryMax: retryMax, MonthRetryMax: monthRetryMax,
			ProgressEvery: 25, Location: w.cfg.App.Timezone,
		}, w.logger)
		if err != nil {
			w.notifyFailure("create daily detail runner: " + err.Error())
			return
		}
		counters, err := runner.Run(ctx, detail.RunID)
		if err != nil {
			if errors.Is(err, storage.ErrDailyDetailNotRunnable) {
				w.logger.Info("daily detail run was claimed by another process", "run_id", detail.RunID)
				return
			}
			if errors.Is(err, context.Canceled) {
				w.logger.Info("daily detail run checkpointed during worker shutdown", "run_id", detail.RunID)
				return
			}
			w.logger.Error("worker daily detail run failed", "run_id", detail.RunID, "error", err)
			w.notifyFailure("daily detail run " + detail.RunID + " failed: " + err.Error())
			return
		}
		// daily_usages 已更新。全校空房除数依赖该汇总，必须刷新。
		if err := storage.RefreshCampusRollup(context.Background(), w.repo.Pool); err != nil {
			w.logger.Error("refresh campus rollup", "run_id", detail.RunID, "error", err)
		}
		if err := storage.RefreshRollupsForDailyDetailRun(context.Background(), w.repo.Pool, detail.RunID); err != nil {
			w.logger.Error("refresh consumption rollups after daily detail", "run_id", detail.RunID, "error", err)
		}
		w.logger.Info("official daily detail collection completed", "run_id", detail.RunID, "counters", counters)
	}()
}

func (w *worker) billSettings(configured storage.BatchScannerSettings) storage.BillRunSettings {
	return storage.BillRunSettings{
		QPS: configured.QPS, Concurrency: configured.Concurrency, RetryMax: configured.RetryMax,
		MonthRetryMax: configured.MonthRetryMax, TimeoutSeconds: int(w.cfg.Scan.Timeout.Seconds()),
	}
}

func (w *worker) detailSettings(configured storage.DetailScannerSettings, initialization bool) storage.DailyDetailRunSettings {
	return storage.DailyDetailRunSettings{
		QPS: configured.QPS, Concurrency: configured.Concurrency,
		RetryMax: configured.RetryMax, MonthRetryMax: configured.MonthRetryMax,
		TimeoutSeconds: int(w.cfg.Scan.Timeout.Seconds()), BootstrapFrom: configured.BootstrapFrom,
		Initialization: initialization,
	}
}

func (w *worker) shutdown() {
	w.mu.Lock()
	w.stopping = true
	w.mu.Unlock()
	w.scanWG.Wait()
}

func (w *worker) maintenance(ctx context.Context) {
	if err := storage.PruneOperationalData(ctx, w.repo.Pool, w.cfg.Scan.ResultRetention, w.cfg.Scan.LogRetention); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("prune operational data", "error", err)
	}
	if err := storage.PruneEmailCodes(ctx, w.repo.Pool); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("prune email codes", "error", err)
	}
	// 补充刷新物化视图。
	// 日明细导入、盘点导入与改阈值各自刷新。
	// 此处覆盖上述入口遗漏的情况。
	if err := storage.RefreshCampusRollup(ctx, w.repo.Pool); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Error("refresh campus rollup", "error", err)
	}
}

func (w *worker) runPush(ctx context.Context) {
	if w.push == nil {
		return
	}
	w.push.Run(ctx)
}

func (w *worker) notifyFailure(message string) {
	if w.mailer == nil || w.cfg.Mail.AdminTo == "" {
		return
	}
	if _, err := w.mailer.SendWorkerFailure(context.Background(), w.cfg.Mail.AdminTo, message); err != nil {
		w.logger.Error("send worker failure notification", "error", err)
	}
}

func fatal(operation string, err error) {
	slog.Error(operation, "error", err)
	os.Exit(1)
}
