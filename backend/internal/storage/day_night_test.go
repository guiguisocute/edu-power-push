package storage

import (
	"math/big"
	"testing"
	"time"
)

/*
区间归属错误时不报错。时区差会把白天记到夜间。
必须覆盖各边界情形。
*/
func TestClassifyDayNight(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("时区数据不可用：%v", err)
	}
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 7, day, hour, minute, 0, 0, loc)
	}
	cases := []struct {
		name string
		from time.Time
		to   time.Time
		want string
	}{
		{"上游两次抄表之间的白天段", at(20, 6, 57), at(20, 17, 41), "day"},
		{"上游两次抄表之间的夜间段", at(20, 17, 41), at(21, 6, 57), "night"},
		{"正好贴着 06:00 与 18:00 的白天", at(20, 6, 0), at(20, 18, 0), "day"},
		{"正好贴着 18:00 与次日 06:00 的夜间", at(20, 18, 0), at(21, 6, 0), "night"},
		{"凌晨那一半仍算夜间", at(21, 1, 0), at(21, 5, 0), "night"},
		{"多数落在白天的短区间仍算白天", at(20, 5, 30), at(20, 9, 0), "day"},
		{"两侧各占一半 → 丢弃", at(20, 15, 0), at(20, 21, 0), ""},
		{"跨过 18:00 边界 → 丢弃", at(20, 16, 0), at(20, 20, 0), ""},
		{"整整一天 → 丢弃（超出时长上限）", at(20, 7, 0), at(21, 7, 0), ""},
		{"零长度区间 → 丢弃", at(20, 9, 0), at(20, 9, 0), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := classifyDayNight(tc.from, tc.to, loc); got != tc.want {
				t.Fatalf("classifyDayNight = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// 库中存 UTC。同一瞬间换 UTC 后必须仍归同一侧。
// 禁止把本地 6–18 点当成 UTC 6–18 点。
func TestClassifyDayNightAcceptsUTCInput(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("时区数据不可用：%v", err)
	}
	from := time.Date(2026, 7, 20, 6, 57, 0, 0, loc).UTC()
	to := time.Date(2026, 7, 20, 17, 41, 0, 0, loc).UTC()
	if got, _ := classifyDayNight(from, to, loc); got != "day" {
		t.Fatalf("UTC 输入归到了 %q，期望 day", got)
	}
	/* 同钟点按 UTC 理解会跨本地 18:00。两侧不纯，必须丢弃。
	   用于发现 loc 被误写为 UTC。 */
	if got, _ := classifyDayNight(
		time.Date(2026, 7, 20, 6, 57, 0, 0, time.UTC),
		time.Date(2026, 7, 20, 17, 41, 0, 0, time.UTC),
		loc,
	); got != "" {
		t.Fatalf("跨本地边界的区间归到了 %q，期望丢弃", got)
	}
}

func TestDayNightAccumulator(t *testing.T) {
	loc := time.UTC
	acc := newDayNightAcc()
	add := func(kwh string, day, fromHour, toHour int) {
		t.Helper()
		rat, ok := new(big.Rat).SetString(kwh)
		if !ok {
			t.Fatalf("无法解析 %q", kwh)
		}
		acc.add(rat, time.Date(2026, 7, day, fromHour, 0, 0, 0, loc), time.Date(2026, 7, day, toHour, 0, 0, 0, loc), 1)
	}
	add("1.2500", 20, 6, 18)
	add("2.7500", 21, 6, 18)
	view := acc.view()
	if view.KWh != "4.0000" {
		t.Fatalf("合计 = %q，期望 4.0000", view.KWh)
	}
	if view.Hours != 24 {
		t.Fatalf("时长 = %v，期望 24", view.Hours)
	}
	if view.Intervals != 2 || view.Days != 2 {
		t.Fatalf("区间/天数 = %d/%d，期望 2/2", view.Intervals, view.Days)
	}
	// 每小时功率必须按实际时长计算。禁止按固定 12 小时窗口。
	if view.KWhPerHour != "0.1667" {
		t.Fatalf("每小时 = %q，期望 0.1667", view.KWhPerHour)
	}
}
