package render

import (
	"strings"
	"testing"
)

// 杀死变异：恢复 quad 局部 UV 或删除 alpha discard 会重新引入材质接缝或绘制透明像素。
func TestTerrainShaderUsesWorldUVAndAlphaCutout(t *testing.T) {
	source := string(terrainShader)
	for _, want := range []string{
		"fn face_uv(world: vec3f, axis: u32) -> vec2f",
		"out.uv    = face_uv(world, axis);",
		"if (c.a < 0.5) { discard; }",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("terrain shader 缺少 %q", want)
		}
	}
}
