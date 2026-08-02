package importer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxDailyDetailErrors = 30

var nonnegativeDecimal = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// DailyDetailDay 为 electricdetail 导出中已校验的一日。
// 小数归一为库内四位小数。
type DailyDetailDay struct {
	Date     time.Time
	UsageKWh string
	CostYuan string
}

// DailyDetailRecord 为已校验的电表×月份单元。
// 失败单元只作审计缺口，不写 coverage 或零用电行。
type DailyDetailRecord struct {
	LineNo         int
	MeterNo        string
	Month          time.Time
	OK             bool
	ObservedAt     time.Time
	CoveredThrough time.Time
	RawRowCount    int
	Days           []DailyDetailDay
	Error          string
	ContentHash    string
}

// DailyDetailPlan 为导出 JSONL 的只读审计结果。
// winner 映射保持私有；导入须经 StreamDailyDetailPlan 再读源文件。
type DailyDetailPlan struct {
	SourceName            string   `json:"source_name"`
	SHA256                string   `json:"sha256"`
	TotalLines            int      `json:"total_lines"`
	SuccessLines          int      `json:"success_lines"`
	FailedLines           int      `json:"failed_lines"`
	SupersededFailedLines int      `json:"superseded_failed_lines"`
	DuplicateSuccessLines int      `json:"duplicate_success_lines"`
	DuplicateFailureLines int      `json:"duplicate_failure_lines"`
	UniqueSuccessCells    int      `json:"unique_success_cells"`
	UniqueFailedCells     int      `json:"unique_failed_cells"`
	Meters                int      `json:"meters"`
	DeclaredMeters        int      `json:"declared_meters"`
	DeclaredFailedCells   int      `json:"declared_failed_cells"`
	MissingMeterCells     int      `json:"missing_meter_cells"`
	UnresolvedFailedCells int      `json:"unresolved_failed_cells"`
	DayRows               int      `json:"day_rows"`
	CoveredDays           int      `json:"covered_days"`
	Months                []string `json:"months"`
	Errors                []string `json:"errors,omitempty"`

	successLines map[string]int
	failureLines map[string]int
}

type dailyDetailExportRow struct {
	Meter         string           `json:"meter"`
	Month         string           `json:"month"`
	OK            bool             `json:"ok"`
	StatusCode    int              `json:"status_code"`
	QueriedAt     string           `json:"queried_at"`
	RowCount      *int             `json:"row_count"`
	RawRowCount   *int             `json:"raw_row_count"`
	DaysWithUsage *int             `json:"days_with_usage"`
	Days          []dailyDetailDay `json:"days"`
	Error         string           `json:"error"`
}

type dailyDetailDay struct {
	Date     string `json:"sj"`
	UsageKWh string `json:"ydl"`
	CostYuan string `json:"ydje"`
}

type seenDailyDetail struct {
	line       int
	hash       string
	observedAt time.Time
}

// ValidateDailyDetails 流式审计导出。
// 预期爬取失败（ok=false）保留在 plan。
// 坏 JSON、坏日期、负值与冲突成功重复整文件拒绝。
func ValidateDailyDetails(r io.Reader, sourceName string, loc *time.Location) (DailyDetailPlan, error) {
	if loc == nil {
		return DailyDetailPlan{}, errors.New("daily details: timezone is required")
	}
	plan := DailyDetailPlan{
		SourceName:   sourceName,
		successLines: map[string]int{}, failureLines: map[string]int{},
	}
	hasher := sha256.New()
	scanner := newDailyDetailScanner(io.TeeReader(r, hasher))
	success := map[string]seenDailyDetail{}
	failures := map[string]int{}

	for scanner.Scan() {
		plan.TotalLines++
		record, err := parseDailyDetailLine(scanner.Bytes(), plan.TotalLines, loc)
		if err != nil {
			addDailyDetailError(&plan, fmt.Sprintf("line %d: %v", plan.TotalLines, err))
			continue
		}
		key := dailyDetailKey(record.MeterNo, record.Month)
		if !record.OK {
			plan.FailedLines++
			if _, exists := failures[key]; exists {
				plan.DuplicateFailureLines++
			}
			failures[key] = record.LineNo
			continue
		}

		plan.SuccessLines++
		if previous, exists := success[key]; exists {
			plan.DuplicateSuccessLines++
			if previous.hash != record.ContentHash {
				addDailyDetailError(&plan, fmt.Sprintf(
					"line %d: conflicting successful duplicate for %s %s (previous line %d)",
					record.LineNo, record.MeterNo, record.Month.Format("2006-01"), previous.line,
				))
				continue
			}
			if record.ObservedAt.Before(previous.observedAt) {
				continue
			}
		}
		success[key] = seenDailyDetail{line: record.LineNo, hash: record.ContentHash, observedAt: record.ObservedAt}
	}
	if err := scanner.Err(); err != nil {
		return plan, fmt.Errorf("daily details: read JSONL: %w", err)
	}
	plan.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	if len(plan.Errors) > 0 {
		return plan, fmt.Errorf("daily details: %d validation problem(s); first: %s", len(plan.Errors), plan.Errors[0])
	}

	meters := map[string]bool{}
	months := map[string]bool{}
	for key, item := range success {
		plan.successLines[key] = item.line
		meter, month := splitDailyDetailKey(key)
		meters[meter] = true
		months[month] = true
	}
	for key, line := range failures {
		if _, hasSuccess := success[key]; hasSuccess {
			plan.SupersededFailedLines++
			continue
		}
		plan.failureLines[key] = line
		meter, month := splitDailyDetailKey(key)
		meters[meter] = true
		months[month] = true
	}
	plan.UniqueSuccessCells = len(plan.successLines)
	plan.UniqueFailedCells = len(plan.failureLines)
	plan.Meters = len(meters)
	plan.DeclaredMeters = plan.Meters
	plan.DeclaredFailedCells = plan.UniqueFailedCells
	plan.UnresolvedFailedCells = plan.UniqueFailedCells
	for month := range months {
		plan.Months = append(plan.Months, month)
	}
	sort.Strings(plan.Months)
	if plan.UniqueSuccessCells == 0 {
		return plan, errors.New("daily details: no valid successful month cells")
	}

	// 第二遍仅统计 winner，避免重复行计入报告。
	return plan, nil
}

// StreamDailyDetailPlan 再读源文件并仅发出各单元 winner。
// SHA-256 必须与审计 plan 一致，以发现校验后源文件被改。
func StreamDailyDetailPlan(
	r io.Reader,
	plan *DailyDetailPlan,
	loc *time.Location,
	fn func(DailyDetailRecord) error,
) error {
	if plan == nil || loc == nil || fn == nil {
		return errors.New("daily details: plan, timezone and callback are required")
	}
	hasher := sha256.New()
	scanner := newDailyDetailScanner(io.TeeReader(r, hasher))
	line := 0
	dayRows, coveredDays := 0, 0
	for scanner.Scan() {
		line++
		record, err := parseDailyDetailLine(scanner.Bytes(), line, loc)
		if err != nil {
			return fmt.Errorf("daily details: source changed at line %d: %w", line, err)
		}
		key := dailyDetailKey(record.MeterNo, record.Month)
		winner := plan.failureLines[key]
		if record.OK {
			winner = plan.successLines[key]
		}
		if winner != line {
			continue
		}
		if record.OK {
			dayRows += len(record.Days)
			coveredDays += int(record.CoveredThrough.Sub(record.Month).Hours()/24) + 1
		}
		if err := fn(record); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("daily details: re-read JSONL: %w", err)
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != plan.SHA256 {
		return fmt.Errorf("daily details: source SHA-256 changed after audit (%s != %s)", actualHash, plan.SHA256)
	}
	plan.DayRows = dayRows
	plan.CoveredDays = coveredDays
	return nil
}

func newDailyDetailScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	return scanner
}

func parseDailyDetailLine(raw []byte, line int, loc *time.Location) (DailyDetailRecord, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return DailyDetailRecord{}, errors.New("blank JSONL line")
	}
	var input dailyDetailExportRow
	// 忽略展示字段与预计算合计；规范值由已校验日行重建。
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&input); err != nil {
		return DailyDetailRecord{}, fmt.Errorf("invalid JSON: %w", err)
	}
	meter := strings.TrimSpace(input.Meter)
	if meter == "" || len(meter) > 64 {
		return DailyDetailRecord{}, errors.New("invalid meter number")
	}
	month, err := time.ParseInLocation("2006-01", strings.TrimSpace(input.Month), loc)
	if err != nil {
		return DailyDetailRecord{}, fmt.Errorf("invalid month %q", input.Month)
	}
	observed, err := parseDailyDetailObservedAt(input.QueriedAt, loc)
	if err != nil {
		return DailyDetailRecord{}, err
	}
	record := DailyDetailRecord{
		LineNo: line, MeterNo: meter, Month: month, OK: input.OK,
		ObservedAt: observed, Error: strings.TrimSpace(input.Error),
	}
	if !input.OK {
		if record.Error == "" {
			record.Error = "crawl record marked unsuccessful"
		}
		return record, nil
	}
	if input.StatusCode != 0 && input.StatusCode != 200 {
		return DailyDetailRecord{}, fmt.Errorf("successful row has HTTP status %d", input.StatusCode)
	}
	covered, err := dailyDetailCoverage(month, observed, loc)
	if err != nil {
		return DailyDetailRecord{}, err
	}
	record.CoveredThrough = covered
	if input.RawRowCount != nil {
		record.RawRowCount = *input.RawRowCount
	} else if input.RowCount != nil {
		record.RawRowCount = *input.RowCount
	}
	if record.RawRowCount < 0 {
		return DailyDetailRecord{}, errors.New("negative raw_row_count")
	}
	if input.RawRowCount != nil && input.RowCount != nil && *input.RawRowCount != *input.RowCount {
		return DailyDetailRecord{}, errors.New("row_count and raw_row_count disagree")
	}

	byDate := map[string]DailyDetailDay{}
	for _, day := range input.Days {
		date, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(day.Date), loc)
		if err != nil || date.Format("2006-01") != month.Format("2006-01") {
			return DailyDetailRecord{}, fmt.Errorf("invalid day %q for month %s", day.Date, month.Format("2006-01"))
		}
		if date.After(covered) {
			return DailyDetailRecord{}, fmt.Errorf("day %s is after covered_through %s", date.Format("2006-01-02"), covered.Format("2006-01-02"))
		}
		usage, err := canonicalDailyDecimal(day.UsageKWh)
		if err != nil {
			return DailyDetailRecord{}, fmt.Errorf("day %s invalid ydl %q: %w", day.Date, day.UsageKWh, err)
		}
		cost, err := canonicalDailyDecimal(day.CostYuan)
		if err != nil {
			return DailyDetailRecord{}, fmt.Errorf("day %s invalid ydje %q: %w", day.Date, day.CostYuan, err)
		}
		key := date.Format("2006-01-02")
		candidate := DailyDetailDay{Date: date, UsageKWh: usage, CostYuan: cost}
		if previous, exists := byDate[key]; exists {
			if previous.UsageKWh != candidate.UsageKWh || previous.CostYuan != candidate.CostYuan {
				return DailyDetailRecord{}, fmt.Errorf("conflicting duplicate day %s", key)
			}
			continue
		}
		byDate[key] = candidate
	}
	for _, day := range byDate {
		record.Days = append(record.Days, day)
	}
	sort.Slice(record.Days, func(i, j int) bool { return record.Days[i].Date.Before(record.Days[j].Date) })
	if input.DaysWithUsage != nil && *input.DaysWithUsage != len(record.Days) {
		return DailyDetailRecord{}, fmt.Errorf("days_with_usage=%d but validated days=%d", *input.DaysWithUsage, len(record.Days))
	}
	if record.RawRowCount < len(record.Days) {
		return DailyDetailRecord{}, fmt.Errorf("raw_row_count=%d is smaller than data days=%d", record.RawRowCount, len(record.Days))
	}
	hashInput := struct {
		Covered string             `json:"covered"`
		Days    []canonicalHashDay `json:"days"`
	}{Covered: covered.Format("2006-01-02")}
	for _, day := range record.Days {
		hashInput.Days = append(hashInput.Days, canonicalHashDay{
			Date: day.Date.Format("2006-01-02"), UsageKWh: day.UsageKWh, CostYuan: day.CostYuan,
		})
	}
	encoded, _ := json.Marshal(hashInput)
	sum := sha256.Sum256(encoded)
	record.ContentHash = hex.EncodeToString(sum[:])
	return record, nil
}

type canonicalHashDay struct {
	Date     string `json:"sj"`
	UsageKWh string `json:"ydl"`
	CostYuan string `json:"ydje"`
}

func parseDailyDetailObservedAt(raw string, loc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty queried_at")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid queried_at %q", raw)
}

func dailyDetailCoverage(month, observed time.Time, loc *time.Location) (time.Time, error) {
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, loc)
	observed = observed.In(loc)
	observedMonth := time.Date(observed.Year(), observed.Month(), 1, 0, 0, 0, 0, loc)
	if observedMonth.Before(month) {
		return time.Time{}, fmt.Errorf("queried_at %s is before requested month %s", observed.Format(time.RFC3339), month.Format("2006-01"))
	}
	last := month.AddDate(0, 1, -1)
	if observedMonth.After(month) {
		return last, nil
	}
	covered := time.Date(observed.Year(), observed.Month(), observed.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	if covered.Before(month) {
		return time.Time{}, fmt.Errorf("month %s has no settled day at queried_at %s", month.Format("2006-01"), observed.Format(time.RFC3339))
	}
	if covered.After(last) {
		covered = last
	}
	return covered, nil
}

func canonicalDailyDecimal(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !nonnegativeDecimal.MatchString(raw) {
		return "", errors.New("must be a non-negative decimal")
	}
	integer := strings.SplitN(raw, ".", 2)[0]
	if len(strings.TrimLeft(integer, "0")) > 16 {
		return "", errors.New("exceeds numeric(20,4)")
	}
	rat, ok := new(big.Rat).SetString(raw)
	if !ok || rat.Sign() < 0 {
		return "", errors.New("invalid decimal")
	}
	scaled := new(big.Rat).Mul(rat, big.NewRat(10000, 1))
	if scaled.Denom().Cmp(big.NewInt(1)) != 0 {
		return "", errors.New("more than four fractional digits")
	}
	return rat.FloatString(4), nil
}

func dailyDetailKey(meter string, month time.Time) string {
	return meter + "\x00" + month.Format("2006-01")
}

func splitDailyDetailKey(key string) (string, string) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}

func addDailyDetailError(plan *DailyDetailPlan, message string) {
	if len(plan.Errors) < maxDailyDetailErrors {
		plan.Errors = append(plan.Errors, message)
	}
}
