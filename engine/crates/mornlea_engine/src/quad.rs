const SHIFT_X: u32 = 0;
const SHIFT_Y: u32 = 4;
const SHIFT_Z: u32 = 8;
const SHIFT_W: u32 = 12;
const SHIFT_H: u32 = 16;
const SHIFT_FACE: u32 = 20;
const SHIFT_MATERIAL: u32 = 23;
const SHIFT_AO: u32 = 39;
const SHIFT_LIGHT: u32 = 47;
/// 角高度 2 与角高度 3 的位偏移。
///
/// bit 55..62 是 quad 布局里仅存的空闲位（bit 63 仍然留空）。水面 quad 因角高度
/// 逐格不同、本就无法贪心合并，`w`/`h` 恒为 1，于是 bit 12..19 那 8 bit 成为冗余
/// 位，与这 8 bit 合起来正好放下四个 4-bit 角高度：
///
/// | 位 | 内容 |
/// |---|---|
/// | 12..15 | 角 0 高度 |
/// | 16..19 | 角 1 高度 |
/// | 55..58 | 角 2 高度 |
/// | 59..62 | 角 3 高度 |
///
/// **quad 实例仍是 `u64` / 8 字节**——`voxel-visual-presentation` 把这条写成 MUST。
const SHIFT_CORNER2: u32 = 55;
const SHIFT_CORNER3: u32 = 59;

/// 水柱内部（上方也是流体）使用的满格高度原值，实际高度 (15+1)/16 = 1。
pub(crate) const FULL_FLUID_HEIGHT: u8 = 15;

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
#[repr(u8)]
pub(crate) enum Face {
    NegX = 0,
    PosX = 1,
    NegY = 2,
    PosY = 3,
    NegZ = 4,
    PosZ = 5,
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub(crate) struct Quad {
    pub x: u8,
    pub y: u8,
    pub z: u8,
    pub w: u8,
    pub h: u8,
    pub face: Face,
    pub material: u16,
    pub ao: u8,
    pub light: u8,
    /// corners 是水面 quad 四个顶点的 4-bit 高度原值，顺序与 `compute_ao` 的角
    /// 顺序一致：`(du,dv)` 依次取 `(-1,-1) (1,-1) (1,1) (-1,1)`，也就是局部
    /// `(u,v)` 的 `(0,0) (1,0) (1,1) (0,1)` 四个顶点。
    ///
    /// 只有落在该格**顶面那一层**的顶点带高度，其余顶点（侧面的两个下顶点、底面
    /// 的全部四个顶点）语义上就在方块底面，一律记 0。非流体 quad 四项全 0。
    ///
    /// 角 2 在任何朝向下都是一个顶面顶点（顶面四角皆是；两个侧面轴向下 index 2
    /// 都落在上沿），而真流体高度恒 `>= 7`，所以 **bit 55..58 非零 ⟺ 这是一条带
    /// 角高度的水面 quad**——判别不花额外标志位，解包据此还原 `w`/`h`。
    pub corners: [u8; 4],
}

impl Quad {
    pub(crate) fn pack(self) -> u64 {
        assert!((1..=16).contains(&self.w));
        assert!((1..=16).contains(&self.h));
        // 带角高度的 quad 借走 w/h 的 8 bit，因此必须是 1×1；水面本就不合并。
        let (low, high) = if self.corners == [0; 4] {
            (
                u64::from(self.w - 1) << SHIFT_W | u64::from(self.h - 1) << SHIFT_H,
                0,
            )
        } else {
            assert!(self.w == 1 && self.h == 1);
            assert!(self.corners.iter().all(|&corner| corner <= 15));
            (
                u64::from(self.corners[0]) << SHIFT_W | u64::from(self.corners[1]) << SHIFT_H,
                u64::from(self.corners[2]) << SHIFT_CORNER2
                    | u64::from(self.corners[3]) << SHIFT_CORNER3,
            )
        };
        u64::from(self.x) << SHIFT_X
            | u64::from(self.y) << SHIFT_Y
            | u64::from(self.z) << SHIFT_Z
            | low
            | (self.face as u64) << SHIFT_FACE
            | u64::from(self.material) << SHIFT_MATERIAL
            | u64::from(self.ao) << SHIFT_AO
            | u64::from(self.light) << SHIFT_LIGHT
            | high
    }

    /// unpack 是 pack 的逆运算，仅供测试与调试使用。
    ///
    /// 判别靠 bit 55..58（角 2）非零，见 `corners` 的说明。
    #[cfg(test)]
    pub(crate) fn unpack(packed: u64) -> Self {
        let corner2 = ((packed >> SHIFT_CORNER2) & 0xf) as u8;
        let (w, h, corners) = if corner2 == 0 {
            (
                ((packed >> SHIFT_W) & 0xf) as u8 + 1,
                ((packed >> SHIFT_H) & 0xf) as u8 + 1,
                [0; 4],
            )
        } else {
            (
                1,
                1,
                [
                    ((packed >> SHIFT_W) & 0xf) as u8,
                    ((packed >> SHIFT_H) & 0xf) as u8,
                    corner2,
                    ((packed >> SHIFT_CORNER3) & 0xf) as u8,
                ],
            )
        };
        Self {
            x: (packed & 0xf) as u8,
            y: ((packed >> SHIFT_Y) & 0xf) as u8,
            z: ((packed >> SHIFT_Z) & 0xf) as u8,
            w,
            h,
            face: match (packed >> SHIFT_FACE) & 7 {
                0 => Face::NegX,
                1 => Face::PosX,
                2 => Face::NegY,
                3 => Face::PosY,
                4 => Face::NegZ,
                _ => Face::PosZ,
            },
            material: ((packed >> SHIFT_MATERIAL) & 0xffff) as u16,
            ao: ((packed >> SHIFT_AO) & 0xff) as u8,
            light: ((packed >> SHIFT_LIGHT) & 0xff) as u8,
            corners,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{Face, Quad};

    #[test]
    fn pack_matches_go_layout() {
        let quad = Quad {
            x: 3,
            y: 4,
            z: 5,
            w: 6,
            h: 7,
            face: Face::PosY,
            material: 0x1234,
            ao: 0xa5,
            light: 0xbc,
            corners: [0; 4],
        };
        let want = 3u64
            | 4u64 << 4
            | 5u64 << 8
            | 5u64 << 12
            | 6u64 << 16
            | (Face::PosY as u64) << 20
            | 0x1234u64 << 23
            | 0xa5u64 << 39
            | 0xbcu64 << 47;
        assert_eq!(quad.pack(), want);
        assert_eq!(Quad::unpack(want), quad);
    }

    /// 对四个角高度的全部合法组合穷举 pack/unpack 往返。
    ///
    /// 角高度取值域是「顶面顶点的 7..=15」加「非顶面顶点的 0」，而角 2 必须非零
    /// （否则整条 quad 会被判成普通 quad），因此这里对 `{0, 7..=15}` 的四元组做
    /// 全组合、跳过角 2 为 0 的组合。任一角写错位偏移都会让往返对不上。
    #[test]
    fn corner_heights_survive_pack_unpack_round_trip() {
        let values = [0u8, 7, 8, 9, 10, 11, 12, 13, 14, 15];
        let mut checked = 0;
        for &c0 in &values {
            for &c1 in &values {
                for &c2 in &values {
                    for &c3 in &values {
                        if c2 == 0 {
                            continue;
                        }
                        let quad = Quad {
                            x: 1,
                            y: 2,
                            z: 3,
                            w: 1,
                            h: 1,
                            face: Face::PosY,
                            material: 0xbeef,
                            ao: 0x5a,
                            light: 0xa5,
                            corners: [c0, c1, c2, c3],
                        };
                        let packed = quad.pack();
                        assert_eq!(Quad::unpack(packed), quad, "corners={:?}", quad.corners);
                        // quad 实例格式 MUST 保持 8 字节：这里顺带钉死 bit 63 未被占用。
                        assert_eq!(packed >> 63, 0, "corners={:?}", quad.corners);
                        checked += 1;
                    }
                }
            }
        }
        assert_eq!(checked, 10 * 10 * 9 * 10);
    }

    /// 普通 quad 的 w/h 与水面 quad 的角高度共用 bit 12..19，二者必须互不串味：
    /// 一条 16×16 的普通 quad 解包后仍是 16×16、角高度全 0。
    #[test]
    fn plain_quads_keep_width_and_height_semantics() {
        let quad = Quad {
            x: 0,
            y: 0,
            z: 0,
            w: 16,
            h: 16,
            face: Face::PosY,
            material: 7,
            ao: 0,
            light: 0,
            corners: [0; 4],
        };
        assert_eq!(Quad::unpack(quad.pack()), quad);
    }
}
