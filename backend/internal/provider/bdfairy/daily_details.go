package bdfairy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

func (c *Client) QueryDailyDetails(ctx context.Context, meter string, months []string, opts provider.DailyDetailQueryOptions) provider.DailyDetailsResult {
	meter = strings.TrimSpace(meter)
	result := provider.DailyDetailsResult{
		QueriedAt: c.now(), Meter: meter, Status: provider.DailyDetailStatusError,
		MonthsRequested: len(months), Months: []provider.DailyDetailMonth{}, Errors: []string{},
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
			result.Errors = append(result.Errors, fmt.Sprintf("invalid detail month %q", month))
			return result
		}
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	attempts := opts.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	pending := append([]string(nil), months...)
	byMonth := make(map[string]provider.DailyDetailMonth, len(months))
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		items, status, err := c.queryDailyDetailsOnce(ctx, meter, pending, opts)
		result.Attempts = attempt
		if err != nil {
			lastErr = err
			result.Status = status
		} else {
			lastErr = nil
			for _, item := range items {
				byMonth[item.Month] = item
			}
			pending = pending[:0]
			for _, month := range months {
				item, ok := byMonth[month]
				if !ok || item.ErrorMessage != "" || item.Status == "error" || item.Status == "partial" {
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
	if len(byMonth) == 0 {
		if lastErr != nil {
			result.Errors = append(result.Errors, lastErr.Error())
		}
		return result
	}
	result.OK = true
	for _, month := range months {
		item, ok := byMonth[month]
		if !ok {
			item = provider.DailyDetailMonth{Month: month, Status: "error", ErrorMessage: "daily detail query did not return a result"}
		}
		result.Months = append(result.Months, item)
		if item.ErrorMessage != "" {
			result.Errors = append(result.Errors, item.Month+": "+item.ErrorMessage)
		}
	}
	if lastErr != nil {
		result.Errors = append(result.Errors, lastErr.Error())
	}
	result.Status = summarizeDailyDetails(result.Months)
	return result
}

func (c *Client) queryDailyDetailsOnce(
	ctx context.Context,
	meter string,
	months []string,
	opts provider.DailyDetailQueryOptions,
) ([]provider.DailyDetailMonth, provider.DailyDetailStatus, error) {
	hc, err := c.newHTTPClient()
	if err != nil {
		return nil, provider.DailyDetailStatusError, err
	}
	hc, err = c.bootstrapGated(ctx, hc, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return nil, provider.DailyDetailStatusError, err
	}
	statusCode, body, err := c.doJSONGated(ctx, hc, http.MethodPost, "/selectmeter", map[string]string{
		"comAddress": meter, "hidType": "电表", "room": "",
	}, map[string]string{
		"Origin": c.origin(), "Referer": c.resolve("/login"),
	}, opts.BeforeRequest, opts.AcquireRequest)
	if err != nil {
		return nil, provider.DailyDetailStatusError, fmt.Errorf("select meter: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		return nil, provider.DailyDetailStatusError, fmt.Errorf("select meter returned HTTP %d", statusCode)
	}
	var selected struct {
		URL, Message, Msg, Error string
	}
	if err := json.Unmarshal(body, &selected); err != nil {
		return nil, provider.DailyDetailStatusError, errors.New("select meter returned non-JSON response")
	}
	if selected.URL == "" {
		rejection := firstNonEmpty(selected.Msg, selected.Message, selected.Error)
		if rejection == "" {
			rejection = "missing redirect URL"
		}
		return nil, provider.DailyDetailStatusEmpty, fmt.Errorf("select meter rejected request: %s", truncate(rejection, 120))
	}
	path := selected.URL
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// 访问 meter_main 以绑定已选电表。
	_, _, _ = c.doGated(ctx, hc, http.MethodGet, path, nil, map[string]string{
		"Referer": c.resolve("/selectmeter"),
	}, opts.BeforeRequest, opts.AcquireRequest)

	monthAttempts := opts.MonthMaxAttempts
	if monthAttempts < 1 {
		monthAttempts = 1
	}
	items := make([]provider.DailyDetailMonth, 0, len(months))
	for _, month := range months {
		item := provider.DailyDetailMonth{Month: month, Status: "error", Days: []provider.DailyUsageDay{}}
		for attempt := 1; attempt <= monthAttempts; attempt++ {
			form := url.Values{"month": {month}, "comAddress": {meter}}
			statusCode, monthBody, requestErr := c.doGated(ctx, hc, http.MethodPost, "/electricdetail", strings.NewReader(form.Encode()), map[string]string{
				"Content-Type": "application/x-www-form-urlencoded",
				"Origin":       c.origin(), "Referer": c.resolve("/meter_main"),
				"Accept": "text/html,application/xhtml+xml,*/*;q=0.8",
			}, opts.BeforeRequest, opts.AcquireRequest)
			if requestErr != nil {
				item.ErrorMessage = requestErr.Error()
			} else if statusCode != http.StatusOK {
				item.ErrorMessage = fmt.Sprintf("HTTP %d", statusCode)
			} else if strings.Contains(string(monthBody), "errorApp") || strings.Contains(string(monthBody), "session 已过期") {
				item.ErrorMessage = "session/error page"
			} else {
				days, rawCount, parseErr := ParseElectricDetail(string(monthBody))
				if parseErr != nil {
					item.ErrorMessage = parseErr.Error()
				} else if err := validateDetailDays(month, days); err != nil {
					item.ErrorMessage = err.Error()
				} else {
					item.Days = days
					item.RawRowCount = rawCount
					item.CoveredThrough = dailyDetailPublishedThrough(month, days)
					if len(days) == 0 {
						item.Status = "no_data"
					} else {
						item.Status = "valid"
					}
					item.ErrorMessage = ""
					break
				}
			}
			if ctx.Err() != nil {
				break
			}
			if attempt < monthAttempts {
				if err := waitMonthlyRetry(ctx, attempt); err != nil {
					item.ErrorMessage = err.Error()
					break
				}
			}
		}
		items = append(items, item)
	}
	return items, summarizeDailyDetails(items), nil
}

func dailyDetailPublishedThrough(month string, days []provider.DailyUsageDay) string {
	latest := ""
	for _, day := range days {
		if strings.HasPrefix(day.Date, month+"-") && day.Date > latest {
			latest = day.Date
		}
	}
	return latest
}

func validateDetailDays(month string, days []provider.DailyUsageDay) error {
	for _, day := range days {
		date, err := time.Parse("2006-01-02", day.Date)
		if err != nil || date.Format("2006-01") != month {
			return fmt.Errorf("invalid daily detail date %q for month %s", day.Date, month)
		}
	}
	return nil
}

func summarizeDailyDetails(items []provider.DailyDetailMonth) provider.DailyDetailStatus {
	valid, noData, problems := 0, 0, 0
	for _, item := range items {
		switch item.Status {
		case "valid":
			valid++
		case "no_data":
			noData++
		default:
			problems++
		}
	}
	if problems > 0 {
		return provider.DailyDetailStatusPartial
	}
	if valid == 0 && noData > 0 {
		return provider.DailyDetailStatusNoData
	}
	return provider.DailyDetailStatusValid
}
