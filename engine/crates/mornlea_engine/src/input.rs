const BLOCKS_BYTES: usize = 27 * 4096 * 2;
const HEIGHTS_PRESENT_BYTES: usize = 9;
const HEIGHTS_BYTES: usize = 9 * 256 * 2;
/// 单条 registry 条目的字节数。
///
/// 布局（小端）：`id: u16` | `opaque: u8` | `emission: u8` | `material: [u16; 6]`
/// | `fluid_height: u8` | `light_attenuation: u8`，共 18 字节。
///
/// 后两个字节与 `emission` 同形状——每方块一个字节、由 Go 侧
/// `internal/mesh.BlockProperties` 烘焙、`encodeNativeInput` 按同一顺序写出：
///
/// - `fluid_height`：该格**孤立时**的 4-bit 高度原值 `h_raw`（实际高度 `(h_raw+1)/16`）。
///   `0` 是「非流体」哨兵：`h_raw = 14 - level` 且 `level <= 7`，所以真流体的 `h_raw`
///   恒在 `7..=14`，`0` 永远不会是合法的流体高度，于是不必再额外花一个标志位。
///   Rust 侧只消费这个数，**不知道也不需要知道流体等级**——等级→高度的映射是 Go 的
///   单一真值源（`internal/assets.Registry.FluidHeight`）。
/// - `light_attenuation`：天空光穿过该方块时的额外衰减，留给任务组 4 的光照实现；
///   本批只把字段通上并原样搬运，mesher 不读它。
const REGISTRY_ENTRY_BYTES: usize = 18;
/// registry 条目表的容量上限。
///
/// 35 = Go 侧 `core.AirID..=core.WaterLevel7ID` 的方块编号总数，也就是
/// `internal/assets.NewRegistry()` 烘焙进 mesh snapshot 的条目数；Go 侧对应的
/// `internal/mesh.nativeMaxRegistryEntries` 必须与本常量一起改，两侧各自独立
/// 定义、没有共享常量或生成步骤，只能人手同步（一次跨语言 engine ABI 变更）。
///
/// **注意**：本文件开头 `BLOCKS_BYTES = 27 * 4096 * 2` 里的 27 是 3×3×3 邻域的
/// 区段数，与本常量只是数字撞了，两者语义无关，改一个绝不能牵动另一个。
const MAX_REGISTRY_ENTRIES: usize = 35;

#[derive(Debug, PartialEq, Eq)]
pub(crate) enum InputError {
    Input,
    Registry,
    Emission,
}

#[derive(Debug)]
pub(crate) struct MeshInput<'a> {
    pub section_origin_y: i32,
    pub air_id: u16,
    pub barrier_id: u16,
    pub blocks: &'a [u8],
    pub heights_present: &'a [u8],
    pub heights: &'a [u8],
    pub registry: RegistryView<'a>,
}

impl<'a> MeshInput<'a> {
    pub(crate) fn parse(input: &'a [u8]) -> Result<Self, InputError> {
        let parsed = Self::parse_structural(input)?;
        parsed.validate_registry(true)?;
        Ok(parsed)
    }

    #[cfg(test)]
    pub(crate) fn parse_allowing_overbright(input: &'a [u8]) -> Result<Self, InputError> {
        let parsed = Self::parse_structural(input)?;
        parsed.validate_registry(false)?;
        Ok(parsed)
    }

    pub(crate) fn parse_structural(input: &'a [u8]) -> Result<Self, InputError> {
        if input.len() < 16 || &input[0..4] != b"MGM1" {
            return Err(InputError::Input);
        }
        let registry_count = usize::from(read_u16(input, 8));
        let words_per_row = usize::from(read_u16(input, 10));
        if registry_count == 0
            || registry_count > MAX_REGISTRY_ENTRIES
            || words_per_row != registry_count.div_ceil(64)
        {
            return Err(InputError::Registry);
        }
        let registry_bytes = registry_count
            .checked_mul(REGISTRY_ENTRY_BYTES)
            .ok_or(InputError::Input)?;
        let visibility_bytes = registry_count
            .checked_mul(words_per_row)
            .and_then(|words| words.checked_mul(8))
            .ok_or(InputError::Input)?;
        let expected = 16usize
            .checked_add(BLOCKS_BYTES)
            .and_then(|n| n.checked_add(HEIGHTS_PRESENT_BYTES))
            .and_then(|n| n.checked_add(HEIGHTS_BYTES))
            .and_then(|n| n.checked_add(registry_bytes))
            .and_then(|n| n.checked_add(visibility_bytes))
            .ok_or(InputError::Input)?;
        if input.len() != expected {
            return Err(InputError::Input);
        }

        let blocks_end = 16 + BLOCKS_BYTES;
        let present_end = blocks_end + HEIGHTS_PRESENT_BYTES;
        let heights_end = present_end + HEIGHTS_BYTES;
        if input[blocks_end..present_end]
            .iter()
            .any(|&present| present > 1)
        {
            return Err(InputError::Input);
        }
        let entries_end = heights_end + registry_bytes;
        let registry = RegistryView {
            entries: &input[heights_end..entries_end],
            visibility: &input[entries_end..],
            count: registry_count,
            words_per_row,
        };
        let air_id = read_u16(input, 12);
        let barrier_id = read_u16(input, 14);

        Ok(Self {
            section_origin_y: read_i32(input, 4),
            air_id,
            barrier_id,
            blocks: &input[16..blocks_end],
            heights_present: &input[blocks_end..present_end],
            heights: &input[present_end..heights_end],
            registry,
        })
    }

    pub(crate) fn validate_registry(&self, reject_overbright: bool) -> Result<(), InputError> {
        if self.air_id == self.barrier_id {
            return Err(InputError::Registry);
        }
        self.registry
            .validate(self.air_id, self.barrier_id, reject_overbright)
    }

    pub(crate) fn block(&self, x: i32, y: i32, z: i32) -> u16 {
        let Some((cx, lx)) = neighbor_cell(x) else {
            return self.barrier_id;
        };
        let Some((cy, ly)) = neighbor_cell(y) else {
            return self.barrier_id;
        };
        let Some((cz, lz)) = neighbor_cell(z) else {
            return self.barrier_id;
        };
        let section = (cx * 3 + cy) * 3 + cz;
        let cell = (ly << 8) | (lz << 4) | lx;
        read_u16(self.blocks, (section * 4096 + cell) * 2)
    }

    pub(crate) fn sky_light(&self, x: i32, y: i32, z: i32) -> u8 {
        let Some((cx, lx)) = neighbor_cell(x) else {
            return 0;
        };
        if neighbor_cell(y).is_none() {
            return 0;
        }
        let Some((cz, lz)) = neighbor_cell(z) else {
            return 0;
        };
        let column = cx * 3 + cz;
        if self.heights_present[column] == 0 {
            return 0;
        }
        let highest = read_i16(self.heights, (column * 256 + (lz << 4) + lx) * 2);
        u8::from(self.section_origin_y + y > i32::from(highest)) * 15
    }
}

#[derive(Debug)]
pub(crate) struct RegistryView<'a> {
    entries: &'a [u8],
    visibility: &'a [u8],
    count: usize,
    words_per_row: usize,
}

impl RegistryView<'_> {
    fn validate(
        &self,
        air_id: u16,
        barrier_id: u16,
        reject_overbright: bool,
    ) -> Result<(), InputError> {
        let mut previous = None;
        let mut has_air = false;
        let mut has_barrier = false;
        for index in 0..self.count {
            let offset = index * REGISTRY_ENTRY_BYTES;
            let id = read_u16(self.entries, offset);
            if previous.is_some_and(|previous| previous >= id) || self.entries[offset + 2] > 1 {
                return Err(InputError::Registry);
            }
            if reject_overbright && self.entries[offset + 3] > 15 {
                return Err(InputError::Emission);
            }
            // fluid_height 是 4-bit 高度原值加「0=非流体」哨兵，合法域只有 0..=14；
            // 15 被保留给「上方也是流体」的满格情形，那是 mesher 现算的、不会出现在
            // 条目里，因此这里一并拒绝，免得错误的条目悄悄产生满格水面。
            if self.entries[offset + 16] > 14 {
                return Err(InputError::Registry);
            }
            has_air |= id == air_id;
            has_barrier |= id == barrier_id;
            previous = Some(id);
        }
        if !has_air || !has_barrier {
            return Err(InputError::Registry);
        }
        Ok(())
    }

    fn index(&self, id: u16) -> Option<usize> {
        let mut low = 0;
        let mut high = self.count;
        while low < high {
            let middle = low + (high - low) / 2;
            match read_u16(self.entries, middle * REGISTRY_ENTRY_BYTES).cmp(&id) {
                std::cmp::Ordering::Less => low = middle + 1,
                std::cmp::Ordering::Greater => high = middle,
                std::cmp::Ordering::Equal => return Some(middle),
            }
        }
        None
    }

    pub(crate) fn opaque(&self, id: u16) -> bool {
        self.index(id)
            .is_some_and(|index| self.entries[index * REGISTRY_ENTRY_BYTES + 2] != 0)
    }

    pub(crate) fn emission(&self, id: u16) -> u8 {
        self.index(id)
            .map_or(0, |index| self.entries[index * REGISTRY_ENTRY_BYTES + 3])
    }

    /// fluid_height 返回该方块**孤立时**的 4-bit 高度原值 `h_raw`，非流体返回 `None`。
    ///
    /// 见 `REGISTRY_ENTRY_BYTES` 的布局说明：`0` 是非流体哨兵，真流体恒在 `7..=14`。
    /// 未登记的方块编号同样返回 `None`（与 `opaque`/`emission` 的缺省口径一致）。
    pub(crate) fn fluid_height(&self, id: u16) -> Option<u8> {
        let index = self.index(id)?;
        match self.entries[index * REGISTRY_ENTRY_BYTES + 16] {
            0 => None,
            raw => Some(raw),
        }
    }

    /// light_attenuation 返回天空光穿过该方块时的额外衰减。
    ///
    /// 任务组 4 的光照实现会消费它；本批只保证它跨过 ABI 边界不丢失。
    #[cfg_attr(not(test), expect(dead_code, reason = "留给任务组 4 的天空光衰减"))]
    pub(crate) fn light_attenuation(&self, id: u16) -> u8 {
        self.index(id)
            .map_or(0, |index| self.entries[index * REGISTRY_ENTRY_BYTES + 17])
    }

    pub(crate) fn material(&self, id: u16, face: usize) -> Option<u16> {
        if face >= 6 {
            return None;
        }
        let index = self.index(id)?;
        Some(read_u16(
            self.entries,
            index * REGISTRY_ENTRY_BYTES + 4 + face * 2,
        ))
    }

    pub(crate) fn face_visible(&self, id: u16, adjacent: u16) -> bool {
        let Some(row) = self.index(id) else {
            return false;
        };
        let Some(column) = self.index(adjacent) else {
            return false;
        };
        let offset = (row * self.words_per_row + column / 64) * 8;
        read_u64(self.visibility, offset) & (1 << (column % 64)) != 0
    }
}

fn neighbor_cell(value: i32) -> Option<(usize, usize)> {
    if !(-16..=31).contains(&value) {
        return None;
    }
    let shifted = value + 16;
    Some(((shifted >> 4) as usize, (shifted & 15) as usize))
}

fn read_u16(bytes: &[u8], offset: usize) -> u16 {
    u16::from_le_bytes([bytes[offset], bytes[offset + 1]])
}

fn read_i16(bytes: &[u8], offset: usize) -> i16 {
    i16::from_le_bytes([bytes[offset], bytes[offset + 1]])
}

fn read_i32(bytes: &[u8], offset: usize) -> i32 {
    i32::from_le_bytes([
        bytes[offset],
        bytes[offset + 1],
        bytes[offset + 2],
        bytes[offset + 3],
    ])
}

fn read_u64(bytes: &[u8], offset: usize) -> u64 {
    u64::from_le_bytes([
        bytes[offset],
        bytes[offset + 1],
        bytes[offset + 2],
        bytes[offset + 3],
        bytes[offset + 4],
        bytes[offset + 5],
        bytes[offset + 6],
        bytes[offset + 7],
    ])
}

#[cfg(test)]
pub(crate) mod tests {
    use super::{InputError, MeshInput};

    const BLOCKS_OFFSET: usize = 16;
    const HEIGHTS_PRESENT_OFFSET: usize = BLOCKS_OFFSET + 27 * 4096 * 2;
    const HEIGHTS_OFFSET: usize = HEIGHTS_PRESENT_OFFSET + 9;
    const REGISTRY_OFFSET: usize = HEIGHTS_OFFSET + 9 * 256 * 2;
    /// 测试夹具复用生产常量，避免条目布局再扩容时夹具默默错位。
    pub(crate) const ENTRY_BYTES: usize = super::REGISTRY_ENTRY_BYTES;

    pub(crate) fn valid_input() -> Vec<u8> {
        let mut input = vec![0; REGISTRY_OFFSET + 3 * ENTRY_BYTES + 3 * 8];
        input[0..4].copy_from_slice(b"MGM1");
        input[4..8].copy_from_slice(&(-32_i32).to_le_bytes());
        input[8..10].copy_from_slice(&3_u16.to_le_bytes());
        input[10..12].copy_from_slice(&1_u16.to_le_bytes());
        input[12..14].copy_from_slice(&0_u16.to_le_bytes());
        input[14..16].copy_from_slice(&1_u16.to_le_bytes());

        let center_cell = BLOCKS_OFFSET + (13 * 4096 + ((2 << 8) | (3 << 4) | 1)) * 2;
        input[center_cell..center_cell + 2].copy_from_slice(&0x1234_u16.to_le_bytes());
        input[HEIGHTS_PRESENT_OFFSET + 4] = 1;
        let height = HEIGHTS_OFFSET + (4 * 256 + (5 << 4) + 3) * 2;
        input[height..height + 2].copy_from_slice(&(-33_i16).to_le_bytes());

        for (index, id) in [0_u16, 1, 40000].into_iter().enumerate() {
            let entry = REGISTRY_OFFSET + index * ENTRY_BYTES;
            input[entry..entry + 2].copy_from_slice(&id.to_le_bytes());
            input[entry + 2] = u8::from(id == 1);
            input[entry + 3] = if id == 40000 { 7 } else { 0 };
            for face in 0..6 {
                let material = id.wrapping_add(face as u16);
                input[entry + 4 + face * 2..entry + 6 + face * 2]
                    .copy_from_slice(&material.to_le_bytes());
            }
            // id=40000 冒充一格流体：h_raw=9、额外衰减 1，用来证明这两个新字节
            // 真的跨过了 ABI 边界（0 是非流体哨兵，若编码丢失就会读回 None/0）。
            input[entry + 16] = if id == 40000 { 9 } else { 0 };
            input[entry + 17] = u8::from(id == 40000);
        }
        for (index, word) in [2_u64, 5, 1].into_iter().enumerate() {
            let offset = REGISTRY_OFFSET + 3 * ENTRY_BYTES + index * 8;
            input[offset..offset + 8].copy_from_slice(&word.to_le_bytes());
        }
        input
    }

    /// input_with_registry_entries 造一份除条目数外一切合法的输入,用来把
    /// MAX_REGISTRY_ENTRIES 这个纯数字常量钉在可执行断言上——否则它被改动时
    /// 没有任何测试会变红。
    fn input_with_registry_entries(count: usize) -> Vec<u8> {
        let words_per_row = count.div_ceil(64);
        let mut input = vec![0; REGISTRY_OFFSET + count * ENTRY_BYTES + count * words_per_row * 8];
        input[0..4].copy_from_slice(b"MGM1");
        input[8..10].copy_from_slice(&(count as u16).to_le_bytes());
        input[10..12].copy_from_slice(&(words_per_row as u16).to_le_bytes());
        // air=0、barrier=1,条目 id 取 0..count 保证严格递增。
        input[12..14].copy_from_slice(&0_u16.to_le_bytes());
        input[14..16].copy_from_slice(&1_u16.to_le_bytes());
        for index in 0..count {
            let entry = REGISTRY_OFFSET + index * ENTRY_BYTES;
            input[entry..entry + 2].copy_from_slice(&(index as u16).to_le_bytes());
        }
        input
    }

    /// registry 条目上限必须正好容下 Go 侧 `core.AirID..=core.WaterLevel7ID` 的
    /// 35 个方块编号:少一条,流体就进不了 mesh snapshot、水永远不出面;多一条,
    /// 两侧对输入长度与条目上限的期望就会分叉。
    #[test]
    fn accepts_exactly_thirty_five_registry_entries() {
        assert!(MeshInput::parse(&input_with_registry_entries(35)).is_ok());
        assert_eq!(
            MeshInput::parse(&input_with_registry_entries(36)).unwrap_err(),
            InputError::Registry
        );
    }

    #[test]
    fn parses_unaligned_little_endian_input_without_typed_casts() {
        let input = valid_input();
        let mut unaligned = vec![0xff];
        unaligned.extend_from_slice(&input);
        let parsed = MeshInput::parse(&unaligned[1..]).unwrap();

        assert_eq!(parsed.section_origin_y, -32);
        assert_eq!(parsed.air_id, 0);
        assert_eq!(parsed.barrier_id, 1);
        assert_eq!(parsed.block(1, 2, 3), 0x1234);
        assert_eq!(parsed.block(-17, 0, 0), 1);
        assert_eq!(parsed.sky_light(3, 0, 5), 15);
        assert_eq!(parsed.sky_light(3, 0, -17), 0);
        assert!(parsed.registry.opaque(1));
        assert!(!parsed.registry.opaque(30000));
        assert_eq!(parsed.registry.emission(40000), 7);
        assert_eq!(parsed.registry.emission(30000), 0);
        assert_eq!(parsed.registry.material(40000, 5), Some(40005));
        assert_eq!(parsed.registry.material(40000, 6), None);
        assert_eq!(parsed.registry.fluid_height(40000), Some(9));
        assert_eq!(parsed.registry.fluid_height(1), None);
        assert_eq!(parsed.registry.fluid_height(30000), None);
        assert_eq!(parsed.registry.light_attenuation(40000), 1);
        assert_eq!(parsed.registry.light_attenuation(1), 0);
        assert_eq!(parsed.registry.light_attenuation(30000), 0);
        assert!(parsed.registry.face_visible(1, 40000));
        assert!(!parsed.registry.face_visible(30000, 0));
    }

    #[test]
    fn rejects_wrong_length_and_magic_as_input_errors() {
        let input = valid_input();
        assert_eq!(
            MeshInput::parse(&input[..input.len() - 1]).unwrap_err(),
            InputError::Input
        );
        let mut long = input.clone();
        long.push(0);
        assert_eq!(MeshInput::parse(&long).unwrap_err(), InputError::Input);
        let mut magic = input;
        magic[0] = b'X';
        assert_eq!(MeshInput::parse(&magic).unwrap_err(), InputError::Input);
    }

    #[test]
    fn rejects_malformed_registry_and_overbright_emission() {
        let mut unsorted = valid_input();
        unsorted[REGISTRY_OFFSET..REGISTRY_OFFSET + 2].copy_from_slice(&1_u16.to_le_bytes());
        assert_eq!(
            MeshInput::parse(&unsorted).unwrap_err(),
            InputError::Registry
        );

        let mut duplicate = valid_input();
        duplicate[REGISTRY_OFFSET + ENTRY_BYTES..REGISTRY_OFFSET + ENTRY_BYTES + 2]
            .copy_from_slice(&0_u16.to_le_bytes());
        assert_eq!(
            MeshInput::parse(&duplicate).unwrap_err(),
            InputError::Registry
        );

        let mut bad_opaque = valid_input();
        bad_opaque[REGISTRY_OFFSET + 2] = 2;
        assert_eq!(
            MeshInput::parse(&bad_opaque).unwrap_err(),
            InputError::Registry
        );

        let mut emission = valid_input();
        emission[REGISTRY_OFFSET + 2 * ENTRY_BYTES + 3] = 16;
        assert_eq!(
            MeshInput::parse(&emission).unwrap_err(),
            InputError::Emission
        );

        // fluid_height 的合法域是 0..=14：15 被保留给「上方也是流体」的满格情形，
        // 只能由 mesher 现算，出现在条目里就是编码方写错了。
        let mut fluid_height = valid_input();
        fluid_height[REGISTRY_OFFSET + 2 * ENTRY_BYTES + 16] = 15;
        assert_eq!(
            MeshInput::parse(&fluid_height).unwrap_err(),
            InputError::Registry
        );

        let mut same_air_and_barrier = valid_input();
        same_air_and_barrier[14..16].copy_from_slice(&0_u16.to_le_bytes());
        assert_eq!(
            MeshInput::parse(&same_air_and_barrier).unwrap_err(),
            InputError::Registry
        );
    }
}
