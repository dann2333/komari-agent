package server

import (
	"testing"
	"time"
)

// TestReconnectBackoff 验证重连等待从 1 秒起步翻倍，并以 --reconnect-interval 为上限
func TestReconnectBackoff(t *testing.T) {
	original := flags.ReconnectInterval
	t.Cleanup(func() { flags.ReconnectInterval = original })

	flags.ReconnectInterval = 5
	for _, tt := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 5 * time.Second},
		{10, 5 * time.Second},
	} {
		if got := reconnectBackoff(tt.attempt); got != tt.want {
			t.Errorf("reconnectBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}

	// 上限本身就不超过 1 秒时, 每次都直接用上限
	flags.ReconnectInterval = 1
	for _, attempt := range []int{1, 2, 5} {
		if got := reconnectBackoff(attempt); got != time.Second {
			t.Errorf("上限 1s 时 reconnectBackoff(%d) = %v, want 1s", attempt, got)
		}
	}

	// 0 或负数表示不等待
	for _, interval := range []int{0, -3} {
		flags.ReconnectInterval = interval
		if got := reconnectBackoff(2); got != 0 {
			t.Errorf("上限 %d 时 reconnectBackoff(2) = %v, want 0", interval, got)
		}
	}

	// 退避不能超过上限, 且必须单调不减
	flags.ReconnectInterval = 30
	prev := time.Duration(0)
	for attempt := 1; attempt <= 12; attempt++ {
		got := reconnectBackoff(attempt)
		if got < prev {
			t.Fatalf("reconnectBackoff 出现回退: attempt=%d got=%v prev=%v", attempt, got, prev)
		}
		if got > 30*time.Second {
			t.Fatalf("reconnectBackoff(%d) = %v 超过上限", attempt, got)
		}
		prev = got
	}
}
