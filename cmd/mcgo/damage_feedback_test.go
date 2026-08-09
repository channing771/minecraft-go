//go:build darwin

package main

import (
	"testing"
	"time"
)

func TestDamageFeedbackUsesOnlyConfirmedDecrease(t *testing.T) {
	var feedback damageFeedback
	if got := feedback.Update(12, true, time.Second); got != 0 {
		t.Fatalf("首次确认强度=%v，想要 0", got)
	}
	if got := feedback.Update(7, true, time.Second); got != 1 {
		t.Fatalf("确认下降当帧强度=%v，想要 1", got)
	}
	if got := feedback.Update(8, true, 90*time.Millisecond); got != 0.5 {
		t.Fatalf("回复且淡出 90ms 强度=%v，想要 0.5", got)
	}
	if got := feedback.Update(8, true, 90*time.Millisecond); got != 0 {
		t.Fatalf("完整 180ms 后强度=%v，想要 0", got)
	}
}

func TestDamageFeedbackRepeatedDamageRestartsFullDuration(t *testing.T) {
	var feedback damageFeedback
	feedback.Update(20, true, 0)
	feedback.Update(15, true, 0)
	if got := feedback.Update(15, true, 90*time.Millisecond); got != 0.5 {
		t.Fatalf("首次伤害淡出强度=%v，想要 0.5", got)
	}
	if got := feedback.Update(10, true, time.Second); got != 1 {
		t.Fatalf("连续伤害当帧强度=%v，想要重新为 1", got)
	}
	if got := feedback.Update(10, true, 179*time.Millisecond); got <= 0 {
		t.Fatalf("重置后 179ms 强度=%v，想要仍大于 0", got)
	}
}

func TestDamageFeedbackElapsedBoundsAndReset(t *testing.T) {
	var feedback damageFeedback
	feedback.Update(20, true, 0)
	feedback.Update(10, true, 0)
	if got := feedback.Update(10, true, -time.Second); got != 1 {
		t.Fatalf("负 elapsed 强度=%v，想要保持 1", got)
	}
	if got := feedback.Update(10, false, 0); got != 0 {
		t.Fatalf("not-ready 强度=%v，想要 0", got)
	}
	if got := feedback.Update(4, true, 0); got != 0 {
		t.Fatalf("reset 后首次 ready 强度=%v，想要 0", got)
	}
	feedback.Update(2, true, 0)
	feedback.Reset()
	if feedback != (damageFeedback{}) {
		t.Fatalf("显式 Reset 后状态=%+v，想要零值", feedback)
	}
}
