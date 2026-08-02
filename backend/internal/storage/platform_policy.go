package storage

// nonPlatformBillingBuilding 的电表仍完整采集并持久化。
// 禁止进入用户绑定、校园展示或统计口径。
// 切换到本平台时仅撤销本规则。历史数据无需恢复。
const nonPlatformBillingBuilding = "16栋"

func platformSupportsBuilding(building string) bool {
	return building != nonPlatformBillingBuilding
}
