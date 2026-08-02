package bdfairy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

func TestClientQueryMeter(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	beforeCalls, acquired, released, active := 0, 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/":
			http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: "test"})
			http.Redirect(w, r, "/oauth", http.StatusFound)
		case "/selectarea":
			writeJSON(t, w, map[string]string{"data": "login"})
		case "/selectmeter":
			writeJSON(t, w, map[string]string{"url": "/meter_main"})
		case "/meter_main":
			_, _ = w.Write([]byte(
				"const yffye = ref('8.44');" +
					"const bzdye = ref('0.00');" +
					"const zye = ref('8.44');" +
					"const zydl = ref('1822.71');" +
					"const dbzt = ref('合闸');" +
					"const cbsj = ref('2026-07-25 06:57:03');",
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL)
	client, err := NewClient(base, "83", "示例大学", time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result := client.QueryMeter(context.Background(), "190610000300", provider.QueryOptions{
		MaxAttempts: 1,
		BeforeRequest: func(context.Context) error {
			beforeCalls++
			return nil
		},
		AcquireRequest: func(context.Context) (func(), error) {
			acquired++
			active++
			return func() {
				active--
				released++
			}, nil
		},
	})
	if !result.OK || result.Status != provider.StatusValid {
		t.Fatalf("QueryMeter() = %+v", result)
	}
	if result.Balance.TotalKWh != "1822.71" {
		t.Fatalf("balance = %+v", result.Balance)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /", "POST /selectarea", "POST /selectmeter", "GET /meter_main"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v", calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
	if beforeCalls != len(want) || acquired != len(want) || released != len(want) || active != 0 {
		t.Fatalf("request gate calls before=%d acquired=%d released=%d active=%d", beforeCalls, acquired, released, active)
	}
}

func TestClientClassifiesEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/oauth", http.StatusFound)
		case "/selectarea":
			writeJSON(t, w, map[string]string{"data": "login"})
		case "/selectmeter":
			writeJSON(t, w, map[string]string{"url": "/meter_main"})
		case "/meter_main":
			_, _ = w.Write([]byte("<div id=\"errorApp\">操作失败</div>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL)
	client, _ := NewClient(base, "83", "示例大学", time.Second)
	result := client.QueryMeter(context.Background(), "missing", provider.QueryOptions{MaxAttempts: 1})
	if result.OK || result.Status != provider.StatusError {
		t.Fatalf("QueryMeter() = %+v", result)
	}
}

func TestClientClearsBoundSessionBeforeSelectingMeter(t *testing.T) {
	var mu sync.Mutex
	selectAreaCalls := 0
	phoneUnbindCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: "test"})
			http.Redirect(w, r, "/oauth", http.StatusFound)
		case "/selectarea":
			mu.Lock()
			selectAreaCalls++
			call := selectAreaCalls
			mu.Unlock()
			if call == 1 {
				writeJSON(t, w, map[string]any{"data": "selectmeter", "band": 1})
			} else {
				writeJSON(t, w, map[string]any{"data": "login", "band": ""})
			}
		case "/phonejcbd":
			mu.Lock()
			phoneUnbindCalls++
			mu.Unlock()
			writeJSON(t, w, map[string]any{"ok": true})
		case "/selectmeter":
			writeJSON(t, w, map[string]string{"url": "/meter_main"})
		case "/meter_main":
			_, _ = w.Write([]byte(
				"const yffye = ref('8.44');" +
					"const zye = ref('8.44');" +
					"const zydl = ref('1822.71');" +
					"const cbsj = ref('2026-07-25 06:57:03');",
			))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL)
	client, _ := NewClient(base, "83", "示例大学", time.Second)
	result := client.QueryMeter(context.Background(), "190610000300", provider.QueryOptions{MaxAttempts: 1})
	if !result.OK {
		t.Fatalf("QueryMeter() = %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if selectAreaCalls != 2 || phoneUnbindCalls != 1 {
		t.Fatalf("selectAreaCalls=%d phoneUnbindCalls=%d", selectAreaCalls, phoneUnbindCalls)
	}
}

func TestClientQueryMonthlyBillsPreservesEmptyMonth(t *testing.T) {
	var gateCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: "test"})
			http.Redirect(w, r, "/oauth", http.StatusFound)
		case "/selectarea":
			writeJSON(t, w, map[string]string{"data": "login"})
		case "/selectmeter":
			writeJSON(t, w, map[string]string{"url": "/meter_main"})
		case "/electricbill":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte("<div id=\"electricbill\"></div>"))
				return
			}
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["month"] == "2026-07" {
				writeJSON(t, w, map[string]any{"data": map[string]any{"qcdl": "", "qmdl": "", "ydl": "", "ydje": "", "sj": "2026-07"}})
				return
			}
			writeJSON(t, w, map[string]any{"data": map[string]any{"qcdl": "10.1", "qmdl": 12.3, "ydl": "2.2", "ydje": "1.32", "sj": "2026-06"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, _ := NewClient(base, "83", "示例大学", time.Second)
	result := client.QueryMonthlyBills(context.Background(), "190610000300", []string{"2026-07", "2026-06"}, provider.MonthlyQueryOptions{
		MaxAttempts: 1, MonthMaxAttempts: 1,
		BeforeRequest: func(context.Context) error { gateCalls++; return nil },
	})
	if !result.OK || result.Status != provider.MonthlyStatusValid || result.MonthsRequested != 2 || len(result.Bills) != 2 {
		t.Fatalf("QueryMonthlyBills() = %+v", result)
	}
	if result.Bills[0].Status != "no_data" || result.Bills[1].Status != "data" || result.Bills[1].Data.EndKWh != "12.3" {
		t.Fatalf("bills = %+v", result.Bills)
	}
	if gateCalls != 6 {
		t.Fatalf("gateCalls=%d, want 6", gateCalls)
	}
}

func TestClientQueryMonthlyBillsRetriesOnlyFailedMonthsWithFreshSession(t *testing.T) {
	var gateCalls, rootCalls, julyCalls, juneCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			rootCalls++
			http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: fmt.Sprintf("test-%d", rootCalls)})
			http.Redirect(w, r, "/oauth", http.StatusFound)
		case "/selectarea":
			writeJSON(t, w, map[string]string{"data": "login"})
		case "/selectmeter":
			writeJSON(t, w, map[string]string{"url": "/meter_main"})
		case "/electricbill":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte("<div id=\"electricbill\"></div>"))
				return
			}
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			switch request["month"] {
			case "2026-07":
				julyCalls++
				if julyCalls == 1 {
					_, _ = w.Write([]byte("<html>temporary upstream page</html>"))
					return
				}
			case "2026-06":
				juneCalls++
			}
			writeJSON(t, w, map[string]any{"data": map[string]any{
				"qcdl": "10.1", "qmdl": "12.3", "ydl": "2.2", "ydje": "1.32", "sj": request["month"],
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, _ := NewClient(base, "83", "示例大学", time.Second)
	result := client.QueryMonthlyBills(context.Background(), "190610000300", []string{"2026-07", "2026-06"}, provider.MonthlyQueryOptions{
		MaxAttempts: 2, MonthMaxAttempts: 1,
		BeforeRequest: func(context.Context) error { gateCalls++; return nil },
	})
	if !result.OK || result.Status != provider.MonthlyStatusValid || result.Attempts != 2 || len(result.Errors) != 0 {
		t.Fatalf("QueryMonthlyBills() = %+v", result)
	}
	if len(result.Bills) != 2 || result.Bills[0].Month != "2026-07" || result.Bills[1].Month != "2026-06" {
		t.Fatalf("bills = %+v", result.Bills)
	}
	if rootCalls != 2 || julyCalls != 2 || juneCalls != 1 || gateCalls != 11 {
		t.Fatalf("root=%d july=%d june=%d gate=%d", rootCalls, julyCalls, juneCalls, gateCalls)
	}
}

func TestClientQueryDailyDetails(t *testing.T) {
	var detailForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.SetCookie(w, &http.Cookie{Name: "connect.sid", Value: "test"})
			http.Redirect(w, r, "/oauth", http.StatusFound)
		case "/selectarea":
			writeJSON(t, w, map[string]string{"data": "login"})
		case "/selectmeter":
			writeJSON(t, w, map[string]string{"url": "/meter_main"})
		case "/meter_main":
			_, _ = w.Write([]byte("ok"))
		case "/electricdetail":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			detailForm = r.Form
			_, _ = w.Write([]byte(`<script>comments=ref(JSON.parse('[{"ydl":"9.70","sj":"2026-07-27","ydje":"6.01"},{"ydl":"","sj":"2026-07-27","ydje":""}]'))</script>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, _ := NewClient(base, "83", "示例大学", time.Second)
	client.now = func() time.Time { return time.Date(2026, 7, 28, 1, 0, 0, 0, time.FixedZone("CST", 8*3600)) }
	result := client.QueryDailyDetails(context.Background(), "190610004855", []string{"2026-07"}, provider.DailyDetailQueryOptions{
		MaxAttempts: 1, MonthMaxAttempts: 1, Location: time.FixedZone("CST", 8*3600),
	})
	if !result.OK || result.Status != provider.DailyDetailStatusValid || len(result.Months) != 1 {
		t.Fatalf("result=%+v", result)
	}
	month := result.Months[0]
	if month.CoveredThrough != "2026-07-27" || len(month.Days) != 1 || month.Days[0].UsageKWh != "9.70" {
		t.Fatalf("month=%+v", month)
	}
	if detailForm.Get("month") != "2026-07" || detailForm.Get("comAddress") != "190610004855" {
		t.Fatalf("form=%v", detailForm)
	}
}

func TestDailyDetailPublishedThroughRequiresExplicitRow(t *testing.T) {
	days := []provider.DailyUsageDay{
		{Date: "2026-07-28", UsageKWh: "1.23", CostYuan: "0.76"},
		{Date: "2026-07-27", UsageKWh: "0.00", CostYuan: "0.00"},
	}
	if got := dailyDetailPublishedThrough("2026-07", days); got != "2026-07-28" {
		t.Fatalf("published through=%q, want 2026-07-28", got)
	}
	days = append(days, provider.DailyUsageDay{Date: "2026-07-29", UsageKWh: "0.00", CostYuan: "0.00"})
	if got := dailyDetailPublishedThrough("2026-07", days); got != "2026-07-29" {
		t.Fatalf("explicit zero day was not published: %q", got)
	}
	if got := dailyDetailPublishedThrough("2026-07", nil); got != "" {
		t.Fatalf("empty month published through=%q", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
}
