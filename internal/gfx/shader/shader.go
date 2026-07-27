// Package shader 用 go:embed 把 WGSL 源码打进二进制，
// 免得运行时还要去找文件。
//
// 它与 gfx 分开成两个包：gfx 只认「一段 WGSL 字符串」，不该知道有哪些着色器。
package shader

import _ "embed"

// Triangle 是 M0 验证用的三角形着色器，入口为 vs_main / fs_main。
//
//go:embed triangle.wgsl
var Triangle string
