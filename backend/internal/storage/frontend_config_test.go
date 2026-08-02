package storage

import "testing"

func TestNormalizeFrontendSettingsDefaultsEmptyRoomThreshold(t *testing.T) {
	settings := FrontendConfigSettings{}
	normalizeFrontendSettings(&settings)
	if settings.Display.EmptyRoomThresholdKWH == nil {
		t.Fatal("empty room threshold was not defaulted")
	}
	if got := *settings.Display.EmptyRoomThresholdKWH; got != DefaultEmptyRoomThresholdKWH {
		t.Fatalf("empty room threshold = %q, want %q", got, DefaultEmptyRoomThresholdKWH)
	}
	if settings.Display.RankingRefreshTime != DefaultRankingRefreshTime {
		t.Fatalf("ranking refresh time = %q", settings.Display.RankingRefreshTime)
	}
	if settings.Features.Channels == nil || settings.Features.ChannelComingSoon == nil {
		t.Fatal("channel presentation maps were not initialized")
	}
}

func TestNormalizeFrontendSettingsPreservesEmptyRoomThreshold(t *testing.T) {
	value := "0.8"
	settings := FrontendConfigSettings{
		Display: FrontendDisplay{EmptyRoomThresholdKWH: &value},
	}
	normalizeFrontendSettings(&settings)
	if got := *settings.Display.EmptyRoomThresholdKWH; got != value {
		t.Fatalf("empty room threshold = %q, want %q", got, value)
	}
}

func TestNormalizeFrontendSettingsCompletesChannelOrder(t *testing.T) {
	settings := FrontendConfigSettings{Features: FrontendFeatures{ChannelOrder: []string{"qq", "mail", "qq", "unknown"}}}
	normalizeFrontendSettings(&settings)
	if len(settings.Features.ChannelOrder) != len(defaultFrontendChannelOrder) {
		t.Fatalf("order=%v", settings.Features.ChannelOrder)
	}
	if settings.Features.ChannelOrder[0] != "qq" || settings.Features.ChannelOrder[1] != "mail" {
		t.Fatalf("explicit order was not preserved: %v", settings.Features.ChannelOrder)
	}
}

func TestNormalizeFrontendSettingsCleansChannelCategories(t *testing.T) {
	settings := FrontendConfigSettings{Features: FrontendFeatures{ChannelCategories: []FrontendChannelCategory{
		{ID: " easy ", Name: " 开箱即用 ", EN: " READY ", Desc: " 说明 ", Channels: []string{"mail", "unknown", "mail"}},
		{ID: "easy", Name: "重复 ID", Channels: []string{"telegram"}},
		{ID: "dev", Name: "", Channels: []string{"webhook"}},
		{ID: "bots", Name: "群聊机器人", Channels: []string{"telegram", "mail"}},
	}}}
	normalizeFrontendSettings(&settings)
	got := settings.Features.ChannelCategories
	if len(got) != 2 {
		t.Fatalf("categories=%v", got)
	}
	if got[0].ID != "easy" || got[0].Name != "开箱即用" || got[0].EN != "READY" || got[0].Desc != "说明" {
		t.Fatalf("first category was not trimmed: %+v", got[0])
	}
	if len(got[0].Channels) != 1 || got[0].Channels[0] != "mail" {
		t.Fatalf("unknown/duplicate members survived: %v", got[0].Channels)
	}
	// mail 已被第一个分类收走。此处仅剩 telegram。禁止渠道跨组重复。
	if len(got[1].Channels) != 1 || got[1].Channels[0] != "telegram" {
		t.Fatalf("channel was claimed twice: %v", got[1].Channels)
	}
}

func TestNormalizeFrontendSettingsAlwaysEmitsChannelCategories(t *testing.T) {
	settings := FrontendConfigSettings{}
	normalizeFrontendSettings(&settings)
	// nil 会被深合并当成未配置并回落默认。空数组才表示运维已清空分组。
	if settings.Features.ChannelCategories == nil {
		t.Fatal("channel categories must serialize as [] rather than null")
	}
}
