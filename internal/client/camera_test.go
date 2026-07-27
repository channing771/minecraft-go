package client_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
)

func TestCameraPitchIsClamped(t *testing.T) {
	c := client.Camera{FovY: 1.2, Aspect: 1.6, Near: 0.1, Far: 1000}
	for i := 0; i < 100; i++ {
		c.Rotate(0, 0.5)
	}
	if c.Pitch > math.Pi/2 {
		t.Fatalf("持续仰头后 Pitch = %f，应夹在 π/2 以内", c.Pitch)
	}
	for i := 0; i < 200; i++ {
		c.Rotate(0, -0.5)
	}
	if c.Pitch < -math.Pi/2 {
		t.Fatalf("持续低头后 Pitch = %f，应夹在 -π/2 以内", c.Pitch)
	}
}

func TestCameraForwardMatchesYaw(t *testing.T) {
	c := client.Camera{}
	f := c.Forward()
	if math.Abs(float64(f.Z()+1)) > 1e-5 || math.Abs(float64(f.X())) > 1e-5 {
		t.Fatalf("初始朝向 = %v，想要 (0,0,-1)", f)
	}
	c.Yaw = math.Pi / 2
	f = c.Forward()
	if math.Abs(float64(f.X()+1)) > 1e-5 {
		t.Fatalf("yaw=90° 时朝向 = %v，X 分量应为 -1", f)
	}
}

func TestCameraMoveIsRelativeToYawOnly(t *testing.T) {
	c := client.Camera{}
	c.Rotate(0, -1)
	before := c.Pos.Y()
	c.Move(1, 0, 0)
	if math.Abs(float64(c.Pos.Y()-before)) > 1e-5 {
		t.Fatalf("低头前进后 Y 变了 %f", c.Pos.Y()-before)
	}
}

func TestCameraViewProjIsFinite(t *testing.T) {
	c := client.Camera{
		Pos: mgl32.Vec3{100, 80, -300},
		Yaw: 1.1, Pitch: -0.4,
		FovY: 1.2, Aspect: 1.6, Near: 0.1, Far: 1000,
	}
	for i, v := range c.ViewProj() {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("ViewProj 第 %d 项非有限值: %f", i, v)
		}
	}
}
