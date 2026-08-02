package bdfairy

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

var (
	refPattern          = regexp.MustCompile("(?m)(?:const|let|var)\\s+([A-Za-z_]\\w*)\\s*=\\s*ref\\(\\s*(?:\\\"([^\\\"]*)\\\"|'([^']*)')\\s*\\)")
	billBlockPattern    = regexp.MustCompile("(?s)billData\\s*=\\s*ref\\(\\s*\\{(.*?)\\}\\s*\\)")
	objectStringPattern = regexp.MustCompile("(?m)([A-Za-z_]\\w*)\\s*:\\s*(?:\\\"([^\\\"]*)\\\"|'([^']*)')")
	detailSinglePattern = regexp.MustCompile(`(?s)comments\s*=\s*ref\(\s*JSON\.parse\(\s*'((?:\\.|[^'])*)'\s*\)\s*\)`)
	detailDoublePattern = regexp.MustCompile(`(?s)comments\s*=\s*ref\(\s*JSON\.parse\(\s*"((?:\\.|[^"])*)"\s*\)\s*\)`)
)

func ParseMeterMain(html string) (provider.Balance, error) {
	refs := extractStringFields(refPattern.FindAllStringSubmatch(html, -1))
	if refs["yffye"] == "" && refs["zydl"] == "" {
		return provider.Balance{}, errors.New("meter_main contains no balance reading")
	}
	return provider.Balance{
		PrepaidYuan: refs["yffye"],
		SubsidyYuan: refs["bzdye"],
		TotalYuan:   refs["zye"],
		TotalKWh:    refs["zydl"],
		MeterStatus: refs["dbzt"],
		ReadingTime: refs["cbsj"],
		Room:        refs["roomaddr"],
		ChargeType:  refs["chargeType"],
	}, nil
}

func ParseElectricBill(html string) (*provider.Bill, error) {
	block := billBlockPattern.FindStringSubmatch(html)
	if len(block) < 2 {
		return nil, errors.New("electricbill contains no billData")
	}
	fields := extractStringFields(objectStringPattern.FindAllStringSubmatch(block[1], -1))
	if fields["qcdl"] == "" && fields["qmdl"] == "" && fields["ydl"] == "" && fields["ydje"] == "" {
		return nil, errors.New("electricbill billData is empty")
	}
	refs := extractStringFields(refPattern.FindAllStringSubmatch(html, -1))
	return &provider.Bill{
		Month: refs["month"],
		Data: provider.BillData{
			StartKWh: fields["qcdl"],
			EndKWh:   fields["qmdl"],
			UsageKWh: fields["ydl"],
			CostYuan: fields["ydje"],
		},
	}, nil
}

// ParseElectricDetail 提取上游 Vue 页内嵌 JSON。
// 忽略空占位行；冲突的非空重复拒绝。
func ParseElectricDetail(html string) ([]provider.DailyUsageDay, int, error) {
	match := detailSinglePattern.FindStringSubmatch(html)
	if len(match) < 2 {
		match = detailDoublePattern.FindStringSubmatch(html)
	}
	if len(match) < 2 {
		return nil, 0, errors.New("electricdetail contains no comments JSON")
	}
	raw := unescapeJSLiteral(match[1])
	var rows []provider.DailyUsageDay
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, 0, errors.New("electricdetail comments JSON is invalid")
	}
	byDate := make(map[string]provider.DailyUsageDay, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		row.Date = strings.TrimSpace(row.Date)
		row.UsageKWh = strings.TrimSpace(row.UsageKWh)
		row.CostYuan = strings.TrimSpace(row.CostYuan)
		if row.UsageKWh == "" && row.CostYuan == "" {
			continue
		}
		if row.Date == "" || row.UsageKWh == "" || row.CostYuan == "" {
			return nil, len(rows), errors.New("electricdetail contains an incomplete usage row")
		}
		if previous, ok := byDate[row.Date]; ok {
			if previous.UsageKWh != row.UsageKWh || previous.CostYuan != row.CostYuan {
				return nil, len(rows), errors.New("electricdetail contains conflicting rows for one date")
			}
			continue
		}
		byDate[row.Date] = row
		order = append(order, row.Date)
	}
	out := make([]provider.DailyUsageDay, 0, len(order))
	for _, date := range order {
		out = append(out, byDate[date])
	}
	return out, len(rows), nil
}

func unescapeJSLiteral(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			out.WriteByte(raw[i])
			continue
		}
		i++
		switch raw[i] {
		case '\\', '\'', '"', '/':
			out.WriteByte(raw[i])
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		default:
			// 保留 \uXXXX 等 JSON 转义供 encoding/json。
			out.WriteByte('\\')
			out.WriteByte(raw[i])
		}
	}
	return out.String()
}

func LooksLikeMeterError(html string, statusCode int) bool {
	return statusCode != 200 ||
		strings.Contains(html, "errorApp") ||
		(strings.Contains(html, "操作失败") && !strings.Contains(html, "yffye"))
}

func extractStringFields(matches [][]string) map[string]string {
	out := make(map[string]string, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		value := match[2]
		if value == "" {
			value = match[3]
		}
		out[match[1]] = value
	}
	return out
}
