//! WGSL 内嵌:R2c 起 shader 归属本 crate(`shaders/` 目录),Rust 渲染器
//! 是唯一消费方。路径失效或文件清空由本模块单测(非空且含入口点)兜底。

/// 地形 pass(实例化紧凑 quad)。
pub const TERRAIN: &str = include_str!("../../shaders/terrain.wgsl");
/// 水面 pass(半透明,与 terrain 共享 atlas 与世界坐标 UV)。
pub const WATER: &str = include_str!("../../shaders/water.wgsl");
/// 天空与程序化方块云 pass。
pub const SKY: &str = include_str!("../../shaders/sky.wgsl");
/// GPU culling compute。
pub const CULL: &str = include_str!("../../shaders/cull.wgsl");
/// HiZ mip 链构建 compute。
pub const HIZ_BUILD: &str = include_str!("../../shaders/hiz_build.wgsl");
/// HiZ 深度拷贝 compute。
pub const HIZ_COPY: &str = include_str!("../../shaders/hiz_copy.wgsl");
/// 实体 pass(avatar 与掉落物共用)。
pub const AVATAR: &str = include_str!("../../shaders/avatar.wgsl");
/// 全屏叠加 pass：伤害红边与水下水色共用（uniform 的 edge 位区分两者）。
pub const DAMAGE_OVERLAY: &str = include_str!("../../shaders/damage_overlay.wgsl");
/// 名牌 billboard pass。
pub const NAME_TAG: &str = include_str!("../../shaders/name_tag.wgsl");
/// 调试面板 pass。
pub const DEBUG_PANEL: &str = include_str!("../../shaders/debug_panel.wgsl");
/// HUD(hotbar 家族)pass。
pub const HUD_HOTBAR: &str = include_str!("../../shaders/hotbar.wgsl");

#[cfg(test)]
mod tests {
    use super::*;

    /// 单源存在性:路径失效或文件清空都必须在编译/测试期暴露。
    #[test]
    fn shaders_are_nonempty_and_have_entry_points() {
        for (name, source) in [
            ("terrain", TERRAIN),
            ("water", WATER),
            ("sky", SKY),
            ("cull", CULL),
            ("hiz_build", HIZ_BUILD),
            ("hiz_copy", HIZ_COPY),
            ("avatar", AVATAR),
            ("damage_overlay", DAMAGE_OVERLAY),
            ("name_tag", NAME_TAG),
            ("debug_panel", DEBUG_PANEL),
            ("hud_hotbar", HUD_HOTBAR),
        ] {
            assert!(!source.trim().is_empty(), "{name} 为空");
            assert!(source.contains("fn "), "{name} 缺少入口函数");
        }
    }
}
