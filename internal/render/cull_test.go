package render_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/render"
)

func TestCullDropsBackFaces(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	quads := cubeFaces()
	got := render.RunCullForTest(dev, quads, mgl32.Vec3{8.5, 100, 8.5})
	if len(got) != 1 {
		t.Fatalf("剔除后剩 %d 个面，想要 1（只有 +Y 面朝向相机）", len(got))
	}
	if got[0].Face != mesh.FacePosY {
		t.Fatalf("保留的面朝向 = %d，想要 FacePosY", got[0].Face)
	}
}

func TestCullFromBelowKeepsBottomFace(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	quads := []mesh.Quad{
		testQuad(mesh.FaceNegY),
		testQuad(mesh.FacePosY),
	}
	got := render.RunCullForTest(dev, quads, mgl32.Vec3{8.5, -100, 8.5})
	if len(got) != 1 || got[0].Face != mesh.FaceNegY {
		t.Fatalf("从下方看应只剩 -Y 面，实际 %+v", got)
	}
}

func TestCullPreservesInstanceData(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	want := mesh.Quad{
		X: 3, Y: 11, Z: 7, W: 5, H: 9,
		Face: mesh.FacePosY, Mat: 1234, AO: 0xB4, Light: 0xF3,
	}
	got := render.RunCullForTest(dev, []mesh.Quad{want}, mgl32.Vec3{3, 200, 7})
	if len(got) != 1 {
		t.Fatalf("剔除后剩 %d 个面，想要 1", len(got))
	}
	if got[0] != want {
		t.Fatalf("实例数据被破坏:\n实际 %+v\n期望 %+v", got[0], want)
	}
}

func cubeFaces() []mesh.Quad {
	return []mesh.Quad{
		testQuad(mesh.FaceNegX), testQuad(mesh.FacePosX),
		testQuad(mesh.FaceNegY), testQuad(mesh.FacePosY),
		testQuad(mesh.FaceNegZ), testQuad(mesh.FacePosZ),
	}
}

func testQuad(face mesh.Face) mesh.Quad {
	return mesh.Quad{
		X: 8, Y: 8, Z: 8, W: 1, H: 1,
		Face: face, AO: 0xFF, Light: 0xF0,
	}
}
