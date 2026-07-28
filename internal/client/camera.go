// Package client 负责窗口、输入、相机与区块流式加载。
package client

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

const pitchLimit = float32(math.Pi/2) - 0.01

// Camera 保存展示相机。yaw=0、pitch=0 时朝向 -Z。
type Camera struct {
	Pos        mgl32.Vec3
	Yaw, Pitch float32
	FovY       float32
	Aspect     float32
	Near, Far  float32
}

func (c *Camera) Forward() mgl32.Vec3 {
	cp := float32(math.Cos(float64(c.Pitch)))
	return mgl32.Vec3{
		-float32(math.Sin(float64(c.Yaw))) * cp,
		float32(math.Sin(float64(c.Pitch))),
		-float32(math.Cos(float64(c.Yaw))) * cp,
	}
}

func (c *Camera) Rotate(dYaw, dPitch float32) {
	c.Yaw += dYaw
	c.Pitch = max(-pitchLimit, min(pitchLimit, c.Pitch+dPitch))
}

// Move 仅供尚未迁移到玩家预测的 benchmark 脚本使用。
func (c *Camera) Move(fwd, right, up float32) {
	sy := float32(math.Sin(float64(c.Yaw)))
	cy := float32(math.Cos(float64(c.Yaw)))
	c.Pos = c.Pos.Add(mgl32.Vec3{
		-sy*fwd + cy*right,
		up,
		-cy*fwd - sy*right,
	})
}

func (c *Camera) ViewProj() mgl32.Mat4 {
	view := mgl32.LookAtV(c.Pos, c.Pos.Add(c.Forward()), mgl32.Vec3{0, 1, 0})
	return core.Perspective(c.FovY, c.Aspect, c.Near, c.Far).Mul4(view)
}
