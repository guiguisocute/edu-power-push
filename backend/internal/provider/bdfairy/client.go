package bdfairy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

const (
	wechatUserAgent  = "Mozilla/5.0 (Linux; Android 12; M2012K11AC Build/SKQ1.211006.001; wv) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/107.0.5304.141 Mobile Safari/537.36 XWEB/5169 MMWEBSDK/20220903 MMWEBID/3905 MicroMessenger/8.0.28.2240(0x28001C35) WeChat/arm64 Weixin NetType/WIFI Language/zh_CN ABI/arm64"
	maxResponseBytes = 4 << 20
)

type Client struct {
	baseURL  *url.URL
	areaID   string
	areaName string
	timeout  time.Duration
	now      func() time.Time
}

func NewClient(baseURL *url.URL, areaID, areaName string, timeout time.Duration) (*Client, error) {
	if baseURL == nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, errors.New("upstream base URL must be absolute")
	}
	if timeout <= 0 {
		return nil, errors.New("upstream timeout must be positive")
	}
	copyURL := *baseURL
	copyURL.Path = strings.TrimRight(copyURL.Path, "/")
	copyURL.RawQuery = ""
	copyURL.Fragment = ""
	return &Client{
		baseURL:  &copyURL,
		areaID:   areaID,
		areaName: areaName,
		timeout:  timeout,
		now:      time.Now,
	}, nil
}

func (c *Client) QueryMeter(ctx context.Context, meter string, opts provider.QueryOptions) provider.Result {
	meter = strings.TrimSpace(meter)
	result := provider.Result{
		QueriedAt: c.now(),
		Meter:     meter,
		AreaID:    c.areaID,
		AreaName:  c.areaName,
		Status:    provider.StatusError,
		Errors:    []string{},
	}
	if meter == "" {
		result.Errors = append(result.Errors, "meter is required")
		return result
	}

	attempts := opts.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		balance, bill, status, err := c.queryOnce(ctx, meter, opts)
		if err == nil {
			result.OK = true
			result.Status = provider.StatusValid
			result.Balance = balance
			result.Bill = bill
			return result
		}
		lastErr = err
		result.Status = status
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr != nil {
		result.Errors = append(result.Errors, lastErr.Error())
	}
	return result
}

func (c *Client) QueryMonthlyBills(ctx context.Context, meter string, months []string, opts provider.MonthlyQueryOptions) provider.MonthlyBillsResult {
	meter = strings.TrimSpace(meter)
	result := provider.MonthlyBillsResult{
		QueriedAt: c.now(), Meter: meter, Status: provider.MonthlyStatusError,
		MonthsRequested: len(months), Bills: []provider.MonthlyBillMonth{}, Errors: []string{},
	}
	if meter == "" {
		result.Errors = append(result.Errors, "meter is required")
		return result
	}
	if len(months) == 0 {
		result.Errors = append(result.Errors, "at least one month is required")
		return result
	}
	for _, month := range months {
		if !validBillMonth(month) {
			result.Errors = append(result.Errors, fmt.Sprintf("invalid bill month %q", month))
			return result
		}
	}
	attempts := opts.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	pending := append([]string(nil), months...)
	byMonth := make(map[string]provider.MonthlyBillMonth, len(months))
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		bills, status, err := c.queryMonthlyBillsOnce(ctx, meter, pending, opts)
		result.Attempts = attempt
		if err != nil {
			lastErr = err
			result.Status = status
		} else {
			lastErr = nil
			for _, bill := range bills {
				byMonth[bill.Month] = bill
			}
			pending = pending[:0]
			for _, month := range months {
				bill, ok := byMonth[month]
				if !ok || bill.ErrorMessage != "" || bill.Status == "partial" || bill.Status == "error" {
					pending = append(pending, month)
				}
			}
			if len(pending) == 0 {
				break
			}
		}
		if attempt == attempts || ctx.Err() != nil {
			break
		}
		if err := waitMonthlyRetry(ctx, attempt); err != nil {
			lastErr = err
			break
		}
	}
	if len(byMonth) > 0 {
		result.OK = true
		result.Bills = make([]provider.MonthlyBillMonth, 0, len(months))
		for _, month := range months {
			bill, ok := byMonth[month]
			if !ok {
				bill = provider.MonthlyBillMonth{Month: month, Status: "error", ErrorMessage: "monthly query did not return a result"}
			}
			result.Bills = append(result.Bills, bill)
			if bill.ErrorMessage != "" {
				result.Errors = append(result.Errors, bill.Month+": "+bill.ErrorMessage)
			} else if bill.Status == "partial" {
				result.Errors = append(result.Errors, bill.Month+": incomplete numeric fields")
			}
		}
		if lastErr != nil {
			result.Errors = append(result.Errors, lastErr.Error())
		}
		result.Status = summarizeMonthlyBills(result.Bills)
		return result
	}
	if lastErr != nil {
		result.Errors = append(result.Errors, lastErr.Error())
	}
	return result
}

func (c *Client) queryMonthlyBillsOnce(
	ctx context.Context,
	meter string,
	months []string,
	opts provider.MonthlyQueryOptions,
) ([]provider.MonthlyBillMonth, provider.MonthlyBillStatus, error) {
	hc, err := c.newHTTPClient()
	if err != nil {
		return nil, provider.MonthlyStatusError, err
	}
	hc, err = c.bootstrapGated(ctx, hc, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return nil, provider.MonthlyStatusError, err
	}

	statusCode, body, err := c.doJSONGated(ctx, hc, http.MethodPost, "/selectmeter", map[string]string{
		"comAddress": meter, "hidType": "电表", "room": "",
	}, map[string]string{
		"Origin": c.origin(), "Referer": c.resolve("/login"),
	}, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return nil, provider.MonthlyStatusError, fmt.Errorf("select meter: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, provider.MonthlyStatusError, fmt.Errorf("select meter returned HTTP %d", statusCode)
	}
	var selected struct {
		URL     string
		Message string
		Msg     string
		Error   string
	}
	if err := json.Unmarshal(body, &selected); err != nil {
		return nil, provider.MonthlyStatusError, errors.New("select meter returned non-JSON response")
	}
	if selected.URL == "" {
		rejection := firstNonEmpty(selected.Msg, selected.Message, selected.Error)
		if rejection == "" {
			rejection = "missing redirect URL"
		}
		return nil, provider.MonthlyStatusEmpty, fmt.Errorf("select meter rejected request: %s", truncate(rejection, 120))
	}

	statusCode, body, err = c.doGated(ctx, hc, http.MethodGet, "/electricbill", nil, map[string]string{
		"Referer": c.resolve("/meter_main"),
	}, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return nil, provider.MonthlyStatusError, fmt.Errorf("load electricbill: %w", err)
	}
	if statusCode != http.StatusOK || strings.Contains(string(body), "errorApp") {
		return nil, provider.MonthlyStatusEmpty, fmt.Errorf("electricbill page unavailable (HTTP %d)", statusCode)
	}

	monthAttempts := opts.MonthMaxAttempts
	if monthAttempts < 1 {
		monthAttempts = 1
	}
	bills := make([]provider.MonthlyBillMonth, 0, len(months))
	for _, month := range months {
		bill := provider.MonthlyBillMonth{Month: month, Status: "error"}
		for attempt := 1; attempt <= monthAttempts; attempt++ {
			statusCode, monthBody, requestErr := c.doJSONGated(ctx, hc, http.MethodPost, "/electricbill", map[string]string{
				"month": month, "comAddress": meter,
			}, map[string]string{
				"Origin": c.origin(), "Referer": c.resolve("/electricbill"),
			}, opts.BeforeRequest, opts.AcquireRequest)
			if requestErr != nil {
				bill.ErrorMessage = requestErr.Error()
			} else if statusCode < 200 || statusCode >= 300 {
				bill.ErrorMessage = fmt.Sprintf("HTTP %d", statusCode)
			} else {
				var envelope struct {
					Data any `json:"data"`
				}
				if err := json.Unmarshal(monthBody, &envelope); err != nil {
					bill.ErrorMessage = "non-JSON response"
				} else {
					data, ok := normalizeMonthlyBillData(envelope.Data)
					if !ok {
						bill.ErrorMessage = "response data is not an object"
					} else {
						bill.Data = data
						bill.Status = billDataStatus(data)
						bill.ErrorMessage = ""
						break
					}
				}
			}
			if ctx.Err() != nil {
				break
			}
			if attempt < monthAttempts {
				if err := waitMonthlyRetry(ctx, attempt); err != nil {
					bill.ErrorMessage = err.Error()
					break
				}
			}
		}
		bills = append(bills, bill)
	}
	return bills, summarizeMonthlyBills(bills), nil
}

func waitMonthlyRetry(ctx context.Context, attempt int) error {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 4 {
		attempt = 4
	}
	delay := 250 * time.Millisecond * time.Duration(1<<(attempt-1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) bootstrapGated(
	ctx context.Context,
	hc *http.Client,
	before func(context.Context) error,
	acquire func(context.Context) (func(), error),
) (*http.Client, error) {
	if _, _, err := c.doGated(ctx, hc, http.MethodGet, "/?id="+url.QueryEscape(c.areaID), nil, nil, before, acquire); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}
	statusCode, body, err := c.doJSONGated(ctx, hc, http.MethodPost, "/selectarea", map[string]string{
		"areaId": c.areaID, "areaName": c.areaName,
	}, map[string]string{"Origin": c.origin(), "Referer": c.resolve("/selectArea")}, before, acquire)
	if err != nil {
		return nil, fmt.Errorf("select area: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("select area returned HTTP %d", statusCode)
	}
	var selectedArea struct {
		Data any `json:"data"`
		Band any `json:"band"`
	}
	if err := json.Unmarshal(body, &selectedArea); err != nil {
		return nil, errors.New("select area returned non-JSON response")
	}
	data, band := fmt.Sprint(selectedArea.Data), fmt.Sprint(selectedArea.Band)
	if band != "1" && data != "selectmeter" && data != "selectPhoneMeter" && data != "meter_main" {
		return hc, nil
	}
	_, _, _ = c.doJSONGated(ctx, hc, http.MethodPost, "/phonejcbd", map[string]any{}, map[string]string{
		"Origin": c.origin(), "Referer": c.resolve("/selectPhoneMeter"),
	}, before, acquire)
	fresh, err := c.newHTTPClient()
	if err != nil {
		return nil, err
	}
	if _, _, err := c.doGated(ctx, fresh, http.MethodGet, "/?id="+url.QueryEscape(c.areaID), nil, nil, before, acquire); err != nil {
		return nil, fmt.Errorf("restart session after unbind: %w", err)
	}
	statusCode, _, err = c.doJSONGated(ctx, fresh, http.MethodPost, "/selectarea", map[string]string{
		"areaId": c.areaID, "areaName": c.areaName,
	}, map[string]string{"Origin": c.origin(), "Referer": c.resolve("/selectArea")}, before, acquire)
	if err != nil {
		return nil, fmt.Errorf("select area after unbind: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, fmt.Errorf("select area after unbind returned HTTP %d", statusCode)
	}
	return fresh, nil
}

func (c *Client) doGated(
	ctx context.Context,
	hc *http.Client,
	method, path string,
	body io.Reader,
	headers map[string]string,
	before func(context.Context) error,
	acquire func(context.Context) (func(), error),
) (int, []byte, error) {
	if before != nil {
		if err := before(ctx); err != nil {
			return 0, nil, err
		}
	}
	release := func() {}
	if acquire != nil {
		var err error
		release, err = acquire(ctx)
		if err != nil {
			return 0, nil, err
		}
	}
	defer release()
	return c.do(ctx, hc, method, path, body, headers)
}

func (c *Client) doJSONGated(
	ctx context.Context,
	hc *http.Client,
	method, path string,
	payload any,
	headers map[string]string,
	before func(context.Context) error,
	acquire func(context.Context) (func(), error),
) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	headers["Accept"] = "application/json, text/plain, */*"
	return c.doGated(ctx, hc, method, path, bytes.NewReader(body), headers, before, acquire)
}

func validBillMonth(value string) bool {
	if len(value) != 7 || value[4] != '-' {
		return false
	}
	_, err := time.Parse("2006-01", value)
	return err == nil
}

func normalizeMonthlyBillData(value any) (provider.BillData, bool) {
	raw, ok := value.(map[string]any)
	if !ok {
		return provider.BillData{}, false
	}
	stringValue := func(key string) string {
		value, exists := raw[key]
		if !exists || value == nil {
			return ""
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "null" || text == "undefined" || text == "<nil>" {
			return ""
		}
		return text
	}
	return provider.BillData{
		StartKWh: stringValue("qcdl"), EndKWh: stringValue("qmdl"),
		UsageKWh: stringValue("ydl"), CostYuan: stringValue("ydje"),
		Message: stringValue("msg"), Period: stringValue("sj"),
	}, true
}

func billDataStatus(data provider.BillData) string {
	values := []string{data.StartKWh, data.EndKWh, data.UsageKWh, data.CostYuan}
	present := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			present++
		}
	}
	switch present {
	case 0:
		return "no_data"
	case len(values):
		return "data"
	default:
		return "partial"
	}
}

func summarizeMonthlyBills(bills []provider.MonthlyBillMonth) provider.MonthlyBillStatus {
	data, noData, problems := 0, 0, 0
	for _, bill := range bills {
		switch bill.Status {
		case "data":
			data++
		case "no_data":
			noData++
		default:
			problems++
		}
	}
	if problems > 0 {
		return provider.MonthlyStatusPartial
	}
	if data == 0 && noData > 0 {
		return provider.MonthlyStatusNoData
	}
	return provider.MonthlyStatusValid
}

func (c *Client) queryOnce(ctx context.Context, meter string, opts provider.QueryOptions) (provider.Balance, *provider.Bill, provider.Status, error) {
	hc, err := c.newHTTPClient()
	if err != nil {
		return provider.Balance{}, nil, provider.StatusError, err
	}
	hc, err = c.bootstrapGated(ctx, hc, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return provider.Balance{}, nil, provider.StatusError, err
	}

	statusCode, body, err := c.doJSONGated(ctx, hc, http.MethodPost, "/selectmeter", map[string]string{
		"comAddress": meter, "hidType": "电表", "room": "",
	}, map[string]string{
		"Origin": c.origin(), "Referer": c.resolve("/login"),
	}, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return provider.Balance{}, nil, provider.StatusError, fmt.Errorf("select meter: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return provider.Balance{}, nil, provider.StatusError, fmt.Errorf("select meter returned HTTP %d", statusCode)
	}

	var selected struct {
		URL     string
		Message string
		Msg     string
		Error   string
	}
	if err := json.Unmarshal(body, &selected); err != nil {
		return provider.Balance{}, nil, provider.StatusParseError, errors.New("select meter returned non-JSON response")
	}
	rejection := firstNonEmpty(selected.Msg, selected.Message, selected.Error)
	if rejection != "" && selected.URL == "" {
		return provider.Balance{}, nil, provider.StatusEmpty, fmt.Errorf("select meter rejected request: %s", truncate(rejection, 120))
	}
	path := selected.URL
	if path == "" {
		path = "/meter_main"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	statusCode, body, err = c.doGated(ctx, hc, http.MethodGet, path, nil, map[string]string{
		"Referer": c.resolve("/selectmeter"),
	}, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		// selectmeter 成功后 meter_main 超时通常表示空表。
		return provider.Balance{}, nil, provider.StatusEmpty, fmt.Errorf("load meter_main: %w", err)
	}
	html := string(body)
	if LooksLikeMeterError(html, statusCode) {
		status := provider.StatusEmpty
		if statusCode == http.StatusInternalServerError || strings.Contains(html, "errorApp") {
			status = provider.StatusError
		}
		return provider.Balance{}, nil, status, fmt.Errorf("meter_main contains no data (HTTP %d)", statusCode)
	}
	balance, err := ParseMeterMain(html)
	if err != nil {
		return provider.Balance{}, nil, provider.StatusParseError, err
	}

	var bill *provider.Bill
	if opts.IncludeBill {
		statusCode, body, err = c.doGated(ctx, hc, http.MethodGet, "/electricbill", nil, map[string]string{
			"Referer": c.resolve("/meter_main"),
		}, opts.BeforeRequest, opts.AcquireRequest)
		if err != nil {
			return provider.Balance{}, nil, provider.StatusError, fmt.Errorf("load electricbill: %w", err)
		}
		if statusCode == http.StatusOK && !strings.Contains(string(body), "errorApp") {
			bill, err = ParseElectricBill(string(body))
			if err != nil {
				return provider.Balance{}, nil, provider.StatusParseError, err
			}
		}
	}
	return balance, bill, provider.StatusValid, nil
}

func (c *Client) newHTTPClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	return &http.Client{
		Timeout: c.timeout,
		Jar:     jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func (c *Client) do(
	ctx context.Context,
	hc *http.Client,
	method, path string,
	body io.Reader,
	headers map[string]string,
) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.resolve(path), body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", wechatUserAgent)
	req.Header.Set("X-Requested-With", "com.tencent.mm")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if len(data) > maxResponseBytes {
		return resp.StatusCode, nil, errors.New("upstream response exceeds 4 MiB")
	}
	return resp.StatusCode, data, nil
}

func (c *Client) resolve(path string) string {
	return strings.TrimRight(c.baseURL.String(), "/") + "/" + strings.TrimLeft(path, "/")
}

func (c *Client) origin() string {
	return c.baseURL.Scheme + "://" + c.baseURL.Host
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
