//! WGSL 单源内嵌:双后端共存期间,shader 必须与 Go 渲染器逐字节同源。
//!
//! 这里以 `include_str!` 相对路径直引 `internal/render/shader/` 下的文件,
//! 杜绝复制漂移;R2c 删除 Go 渲染器时再把文件迁入本 crate。相对路径的
//! 脆弱性由本模块单测(非空且含入口点)兜底。

/// 地形 pass(实例化紧凑 quad)。
pub const TERRAIN: &str = include_str!("../../../../../internal/render/shader/terrain.wgsl");
/// 天空与程序化方块云 pass。
pub const SKY: &str = include_str!("../../../../../internal/render/shader/sky.wgsl");
/// GPU culling compute。
pub const CULL: &str = include_str!("../../../../../internal/render/shader/cull.wgsl");
/// HiZ mip 链构建 compute。
pub const HIZ_BUILD: &str = include_str!("../../../../../internal/render/shader/hiz_build.wgsl");
/// HiZ 深度拷贝 compute。
pub const HIZ_COPY: &str = include_str!("../../../../../internal/render/shader/hiz_copy.wgsl");

#[cfg(test)]
mod tests {
    use super::*;

    /// 单源存在性:路径失效或文件清空都必须在编译/测试期暴露。
    #[test]
    fn shaders_are_nonempty_and_have_entry_points() {
        for (name, source) in [
            ("terrain", TERRAIN),
            ("sky", SKY),
            ("cull", CULL),
            ("hiz_build", HIZ_BUILD),
            ("hiz_copy", HIZ_COPY),
        ] {
            assert!(!source.trim().is_empty(), "{name} 为空");
            assert!(source.contains("fn "), "{name} 缺少入口函数");
        }
    }
}
