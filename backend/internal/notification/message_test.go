package notification

import (
	"strings"
	"testing"
	"time"
)

func TestProductMessagesAlwaysContainMeterAndDormFacts(t *testing.T) {
	meter := Meter{Number: "31240718", Building: "12栋", Floor: "4楼", Room: "402"}
	messages := []Message{
		LowBalance(time.Now(), meter, "8.50", "10.00"),
		Digest(time.Now(), meter, "8.50", "3.20", "07-25 至 07-31"),
	}
	for _, message := range messages {
		for _, want := range []string{"电表号：31240718", "宿舍楼栋：12栋", "楼层：4楼", "房间号：402"} {
			if !strings.Contains(message.Body, want) {
				t.Fatalf("%s missing %q: %s", message.Event, want, message.Body)
			}
		}
	}
}
