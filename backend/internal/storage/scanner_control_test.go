package storage

import (
	"encoding/json"
	"testing"
)

func scannerControlDefaults() ScannerControlSettings {
	return ScannerControlSettings{
		Balance: BalanceScannerSettings{Cron: "15 */4 * * *", BoundCron: "5 * * * *", QPS: 4, Concurrency: 4, RetryMax: 2, Enabled: true},
		Bills:   BatchScannerSettings{Cron: "0 19 1 * *", QPS: 10, Concurrency: 6, RetryMax: 2, MonthRetryMax: 2, Enabled: true},
		DailyDetails: DetailScannerSettings{
			BatchScannerSettings: BatchScannerSettings{Cron: "30 6 * * *", QPS: 8, Concurrency: 8, RetryMax: 1, MonthRetryMax: 1, Enabled: true},
			RetryCron:            "15 10 * * *",
			BootstrapFrom:        "2025-01",
		},
	}
}

func TestDecodeLegacyScannerControlSettings(t *testing.T) {
	got, err := decodeScannerControlSettings(json.RawMessage(`{"cron":"0 */12 * * *","qps":3,"concurrency":6,"enabled":false}`), scannerControlDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance.Cron != "0 */12 * * *" || got.Balance.QPS != 3 || got.Balance.Concurrency != 6 || got.Balance.Enabled {
		t.Fatalf("legacy balance settings were not preserved: %+v", got.Balance)
	}
	if got.Bills.QPS != 10 || got.DailyDetails.BootstrapFrom != "2025-01" {
		t.Fatalf("new collectors did not retain environment defaults: %+v", got)
	}
}

func TestDecodeCurrentScannerControlSettingsOverlaysDefaults(t *testing.T) {
	got, err := decodeScannerControlSettings(json.RawMessage(`{"balance":{"qps":2},"bills":{"enabled":false},"daily_details":{"cron":"0 1 * * *"}}`), scannerControlDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if got.Balance.QPS != 2 || got.Balance.Cron != "15 */4 * * *" || got.Balance.BoundCron != "5 * * * *" {
		t.Fatalf("balance overlay lost defaults: %+v", got.Balance)
	}
	if got.Bills.Enabled || got.Bills.QPS != 10 {
		t.Fatalf("bill overlay lost defaults: %+v", got.Bills)
	}
	if got.DailyDetails.Cron != "0 1 * * *" || got.DailyDetails.RetryCron != "15 10 * * *" || got.DailyDetails.BootstrapFrom != "2025-01" {
		t.Fatalf("detail overlay lost defaults: %+v", got.DailyDetails)
	}
}
