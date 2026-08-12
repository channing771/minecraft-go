const SHIFT_X: u32 = 0;
const SHIFT_Y: u32 = 4;
const SHIFT_Z: u32 = 8;
const SHIFT_W: u32 = 12;
const SHIFT_H: u32 = 16;
const SHIFT_FACE: u32 = 20;
const SHIFT_MATERIAL: u32 = 23;
const SHIFT_AO: u32 = 39;
const SHIFT_LIGHT: u32 = 47;

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
}

impl Quad {
    pub(crate) fn pack(self) -> u64 {
        assert!((1..=16).contains(&self.w));
        assert!((1..=16).contains(&self.h));
        u64::from(self.x) << SHIFT_X
            | u64::from(self.y) << SHIFT_Y
            | u64::from(self.z) << SHIFT_Z
            | u64::from(self.w - 1) << SHIFT_W
            | u64::from(self.h - 1) << SHIFT_H
            | (self.face as u64) << SHIFT_FACE
            | u64::from(self.material) << SHIFT_MATERIAL
            | u64::from(self.ao) << SHIFT_AO
            | u64::from(self.light) << SHIFT_LIGHT
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
    }
}
