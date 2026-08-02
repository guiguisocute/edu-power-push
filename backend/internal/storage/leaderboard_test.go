package storage

import "testing"

// 默认完整栋号与楼层。房间打码。名称列另测。
func TestDefaultLocationMasks(t *testing.T) {
	label := rankingLocation(defaultLeaderboardPreference(), "12栋", "4楼", "402", false)
	if label != "12栋 · 4楼 · **" {
		t.Fatalf("label = %q, want %q", label, "12栋 · 4楼 · **")
	}
}

func TestRankingName(t *testing.T) {
	tests := []struct {
		name     string
		binders  int
		showNick bool
		nicks    []string
		reveal   bool
		want     string
	}{
		{"未绑账号", 0, false, nil, false, "未注册用户"},
		{"未绑账号有脏昵称字段", 0, true, []string{"x"}, false, "未注册用户"},
		{"已注册关昵称", 1, false, []string{"林亦"}, false, "匿名用户"},
		{"已注册开昵称", 1, true, []string{"林亦"}, false, "林亦"},
		{"已注册开昵称但空", 1, true, []string{"  "}, false, "匿名用户"},
		{"已注册开昵称无昵称", 1, true, []string{""}, false, "匿名用户"},
		// 合住无唯一昵称归属。开启也不写名字。
		{"两人合住开昵称", 2, true, []string{"林亦", "张三"}, false, "共享房间"},
		{"四人合住关昵称", 4, false, []string{"a", "b", "c", "d"}, false, "共享房间"},
		// 管理员视角不脱敏。列出全部名字。
		{"管理员看合住", 2, false, []string{"林亦", "张三"}, true, "林亦、张三"},
		{"管理员看未填昵称", 2, false, []string{"林亦", " "}, true, "林亦、未命名"},
		{"管理员看单人关昵称", 1, false, []string{"林亦"}, true, "林亦"},
		{"管理员看未注册", 0, false, nil, true, "未注册用户"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rankingName(test.binders, test.showNick, test.nicks, test.reveal); got != test.want {
				t.Errorf("rankingName = %q, want %q", got, test.want)
			}
		})
	}
}

// 管理员视角位置一律完整。展示与打码开关均不生效。
func TestRankingLocationRevealIgnoresPreferences(t *testing.T) {
	pref := LeaderboardPreference{} // 全部关闭。最严设置。
	got := rankingLocation(pref, "12栋", "4楼", "402", true)
	if got != "12栋 · 4楼 · 402" {
		t.Fatalf("reveal location = %q, want %q", got, "12栋 · 4楼 · 402")
	}
}

func TestRankingLocationRespectsPreferences(t *testing.T) {
	tests := []struct {
		name string
		pref LeaderboardPreference
		want string
	}{
		{"全部关闭", LeaderboardPreference{}, ""},
		{
			"打码楼栋楼层房间",
			LeaderboardPreference{
				ShowBuilding: true, ShowFloor: true, ShowRoom: true,
				MaskBuilding: true, MaskFloor: true, MaskRoom: true,
			},
			"**栋 · **楼 · **",
		},
		{
			"完整地址不打码",
			LeaderboardPreference{ShowBuilding: true, ShowFloor: true, ShowRoom: true},
			"12栋 · 4楼 · 402",
		},
		{
			"房间打码保留非数字字面",
			LeaderboardPreference{ShowRoom: true, MaskRoom: true},
			"N**",
		},
		{
			"位置不含昵称",
			LeaderboardPreference{ShowNickname: true, ShowBuilding: true},
			"12栋",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			building, floor, room := "12栋", "4楼", "402"
			if test.name == "房间打码保留非数字字面" {
				room = "N301"
			}
			if got := rankingLocation(test.pref, building, floor, room, false); got != test.want {
				t.Errorf("location = %q, want %q", got, test.want)
			}
		})
	}
}

// 真实楼层字符串很脏。有数字仅打码数字。无数字则整体隐去。
func TestMaskDigits(t *testing.T) {
	tests := map[string]string{
		"12栋":         "**栋",
		"3楼N":         "**楼N",
		"1楼(不是NF，SF)": "**楼(不是NF，SF)",
		"N301":        "N**",
		"SF,NF架空层":    "**",
		"":            "",
	}
	for input, want := range tests {
		if got := maskDigits(input); got != want {
			t.Errorf("maskDigits(%q) = %q, want %q", input, got, want)
		}
	}
}
