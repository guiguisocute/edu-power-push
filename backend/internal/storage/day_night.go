package storage

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

/*
日 / 夜用电拆分。

	上游每日仅两次抄表（cbsj）。无小时级曲线。
	数据源为 consumption_deltas 区间用量。
	整段落在 06:00–18:00 记白天。18:00–次日 06:00 记夜间。
	跨边界区间一律丢弃。禁止按时长比例分摊。
	拆分结果不保证可用。availability 据此判定。
	两侧时长不等。比较强度用每小时功率。比较总量用合计。
*/
const (
	dayNightDayStart   = 6
	dayNightNightStart = 18
	// 两侧各至少覆盖这么多天。否则对比无意义。
	dayNightMinDays = 3
	// 区间时长范围。过短无法判时段。过长（≥ 一天）必跨两侧。
	dayNightMinHours = 2.0
	dayNightMaxHours = 20.0
	// 同侧时长占比门槛。达到后才打标签。
	dayNightMinPurity = 0.85
)

type DayNightSlice struct {
	KWh string `json:"kwh"`
	/** 该侧计入区间总时长（小时）。用于算每小时功率。 */
	Hours      float64 `json:"hours"`
	KWhPerHour string  `json:"kwh_per_hour"`
	Intervals  int     `json:"intervals"`
	Days       int     `json:"days"`
	/** 计入时长中落在本侧窗口的比例。1 表示边界完全对齐。 */
	Purity float64 `json:"purity"`
}

type DayNightView struct {
	Meter       string        `json:"meter"`
	From        time.Time     `json:"from"`
	To          time.Time     `json:"to"`
	Timezone    string        `json:"timezone"`
	DayWindow   string        `json:"day_window"`
	NightWindow string        `json:"night_window"`
	Day         DayNightSlice `json:"day"`
	Night       DayNightSlice `json:"night"`
	/** 跨 06:00 / 18:00 边界且无法归属而丢弃的区间数。 */
	SkippedIntervals int    `json:"skipped_intervals"`
	Availability     string `json:"availability"`
}

type dayNightAcc struct {
	kwh       *big.Rat
	hours     float64
	pureHours float64
	intervals int
	days      map[string]struct{}
}

func newDayNightAcc() *dayNightAcc {
	return &dayNightAcc{kwh: new(big.Rat), days: map[string]struct{}{}}
}

func (a *dayNightAcc) add(kwh *big.Rat, from, to time.Time, purity float64) {
	hours := to.Sub(from).Hours()
	a.kwh.Add(a.kwh, kwh)
	a.hours += hours
	a.pureHours += hours * purity
	a.intervals++
	a.days[from.Format("2006-01-02")] = struct{}{}
}

func (a *dayNightAcc) view() DayNightSlice {
	round2 := func(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
	slice := DayNightSlice{
		KWh:       a.kwh.FloatString(4),
		Hours:     round2(a.hours),
		Intervals: a.intervals,
		Days:      len(a.days),
		Purity:    1,
	}
	if a.hours > 0 {
		rate := new(big.Rat).Quo(a.kwh, new(big.Rat).SetFloat64(a.hours))
		slice.KWhPerHour = rate.FloatString(4)
		slice.Purity = round2(a.pureHours / a.hours)
	} else {
		slice.KWhPerHour = "0.0000"
	}
	return slice
}

/*
dayOverlapHours 返回区间与各 [06:00, 18:00) 白天窗口的交集时长。

	夜间时长 = 总时长 − 白天时长。无需单独计算。
*/
func dayOverlapHours(f, t time.Time, loc *time.Location) float64 {
	total := 0.0
	// 从前一天开始扫。区间起点可落在凌晨（昨夜）。
	cursor := time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	for cursor.Before(t) {
		start := time.Date(cursor.Year(), cursor.Month(), cursor.Day(), dayNightDayStart, 0, 0, 0, loc)
		end := time.Date(cursor.Year(), cursor.Month(), cursor.Day(), dayNightNightStart, 0, 0, 0, loc)
		if start.Before(f) {
			start = f
		}
		if end.After(t) {
			end = t
		}
		if end.After(start) {
			total += end.Sub(start).Hours()
		}
		cursor = cursor.AddDate(0, 0, 1)
	}
	return total
}

/*
classifyDayNight 判断抄表区间归属白天或夜间，并返回纯度。

	按主要落在哪一侧打标签。禁止按时长比例分摊用电。
	纯度随结果返回。低于门槛则丢弃。
*/
func classifyDayNight(from, to time.Time, loc *time.Location) (string, float64) {
	f := from.In(loc)
	t := to.In(loc)
	if !t.After(f) {
		return "", 0
	}
	hours := t.Sub(f).Hours()
	// 过短无法判时段。过长必跨两侧。
	if hours < dayNightMinHours || hours > dayNightMaxHours {
		return "", 0
	}
	dayHours := dayOverlapHours(f, t, loc)
	nightHours := hours - dayHours
	side, purity := "night", nightHours/hours
	if dayHours >= nightHours {
		side, purity = "day", dayHours/hours
	}
	if purity < dayNightMinPurity {
		return "", 0
	}
	return side, purity
}

/*
GetDayNightSplit 统计一块表在窗口内的日 / 夜用电。

	仅取 status 为 valid / unchanged 的区间。
	unchanged 为真实 0 度，不是缺数据。异常段不参与。
*/
func GetDayNightSplit(
	ctx context.Context,
	pool *pgxpool.Pool,
	meter string,
	from, to time.Time,
	loc *time.Location,
) (DayNightView, error) {
	if loc == nil {
		loc = time.UTC
	}
	result := DayNightView{
		Meter:       meter,
		From:        from,
		To:          to,
		Timezone:    loc.String(),
		DayWindow:   fmt.Sprintf("%02d:00-%02d:00", dayNightDayStart, dayNightNightStart),
		NightWindow: fmt.Sprintf("%02d:00-%02d:00", dayNightNightStart, dayNightDayStart),
	}
	rows, err := pool.Query(ctx, `
		SELECT d.from_time, d.to_time, d.delta_kwh::text
		FROM consumption_deltas d JOIN meters m ON m.id = d.meter_id
		WHERE m.meter_no = $1
		  AND d.status IN ('valid', 'unchanged')
		  AND d.delta_kwh IS NOT NULL
		  AND d.from_time >= $2 AND d.to_time <= $3
		ORDER BY d.from_time`, meter, from, to)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	day := newDayNightAcc()
	night := newDayNightAcc()
	for rows.Next() {
		var fromTime, toTime time.Time
		var raw string
		if err := rows.Scan(&fromTime, &toTime, &raw); err != nil {
			return result, err
		}
		kwh, ok := new(big.Rat).SetString(raw)
		if !ok {
			result.SkippedIntervals++
			continue
		}
		side, purity := classifyDayNight(fromTime, toTime, loc)
		switch side {
		case "day":
			day.add(kwh, fromTime.In(loc), toTime.In(loc), purity)
		case "night":
			night.add(kwh, fromTime.In(loc), toTime.In(loc), purity)
		default:
			result.SkippedIntervals++
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	result.Day = day.view()
	result.Night = night.view()
	switch {
	case day.intervals == 0 && night.intervals == 0:
		result.Availability = "unavailable"
	case len(day.days) < dayNightMinDays || len(night.days) < dayNightMinDays:
		result.Availability = "insufficient_history"
	default:
		result.Availability = "ready"
	}
	return result, nil
}
