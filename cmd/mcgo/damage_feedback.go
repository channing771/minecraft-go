//go:build darwin

package main

import "time"

const damageFeedbackDuration = 180 * time.Millisecond

// damageFeedback 只根据确认生命值维护本地呈现计时，不预测任何伤害。
type damageFeedback struct {
	hasHealth bool
	health    uint8
	remaining time.Duration
}

// Update 接收本帧确认生命值并返回 0..1 的遮罩强度。
func (feedback *damageFeedback) Update(
	health uint8,
	ready bool,
	elapsed time.Duration,
) float32 {
	if !ready {
		feedback.Reset()
		return 0
	}
	if !feedback.hasHealth {
		feedback.hasHealth = true
		feedback.health = health
		return 0
	}
	damaged := health < feedback.health
	feedback.health = health
	if damaged {
		feedback.remaining = damageFeedbackDuration
		return 1
	}
	if elapsed > 0 {
		if elapsed >= feedback.remaining {
			feedback.remaining = 0
		} else {
			feedback.remaining -= elapsed
		}
	}
	return float32(feedback.remaining) / float32(damageFeedbackDuration)
}

// Reset 清除当前会话的确认基线与呈现计时。
func (feedback *damageFeedback) Reset() {
	*feedback = damageFeedback{}
}
