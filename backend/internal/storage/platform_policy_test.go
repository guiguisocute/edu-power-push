package storage

import "testing"

func TestPlatformSupportsBuilding(t *testing.T) {
	if platformSupportsBuilding("16栋") {
		t.Fatal("16栋当前不应进入用户绑定或校园统计口径")
	}
	for _, building := range []string{"1栋", "15栋", "17栋"} {
		if !platformSupportsBuilding(building) {
			t.Fatalf("%s 被错误排除", building)
		}
	}
}
