const BLOCKS_BYTES: usize = 27 * 4096 * 2;
const HEIGHTS_PRESENT_BYTES: usize = 9;
const HEIGHTS_BYTES: usize = 9 * 256 * 2;
const REGISTRY_ENTRY_BYTES: usize = 16;
const MAX_REGISTRY_ENTRIES: usize = 27;

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
        if air_id == barrier_id {
            return Err(InputError::Registry);
        }
        registry.validate(air_id, barrier_id)?;

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
    fn validate(&self, air_id: u16, barrier_id: u16) -> Result<(), InputError> {
        let mut previous = None;
        let mut has_air = false;
        let mut has_barrier = false;
        for index in 0..self.count {
            let offset = index * REGISTRY_ENTRY_BYTES;
            let id = read_u16(self.entries, offset);
            if previous.is_some_and(|previous| previous >= id) || self.entries[offset + 2] > 1 {
                return Err(InputError::Registry);
            }
            if self.entries[offset + 3] > 15 {
                return Err(InputError::Emission);
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

    pub(crate) fn valid_input() -> Vec<u8> {
        let mut input = vec![0; REGISTRY_OFFSET + 3 * 16 + 3 * 8];
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
            let entry = REGISTRY_OFFSET + index * 16;
            input[entry..entry + 2].copy_from_slice(&id.to_le_bytes());
            input[entry + 2] = u8::from(id == 1);
            input[entry + 3] = if id == 40000 { 7 } else { 0 };
            for face in 0..6 {
                let material = id.wrapping_add(face as u16);
                input[entry + 4 + face * 2..entry + 6 + face * 2]
                    .copy_from_slice(&material.to_le_bytes());
            }
        }
        for (index, word) in [2_u64, 5, 1].into_iter().enumerate() {
            let offset = REGISTRY_OFFSET + 3 * 16 + index * 8;
            input[offset..offset + 8].copy_from_slice(&word.to_le_bytes());
        }
        input
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
        duplicate[REGISTRY_OFFSET + 16..REGISTRY_OFFSET + 18].copy_from_slice(&0_u16.to_le_bytes());
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
        emission[REGISTRY_OFFSET + 2 * 16 + 3] = 16;
        assert_eq!(
            MeshInput::parse(&emission).unwrap_err(),
            InputError::Emission
        );

        let mut same_air_and_barrier = valid_input();
        same_air_and_barrier[14..16].copy_from_slice(&0_u16.to_le_bytes());
        assert_eq!(
            MeshInput::parse(&same_air_and_barrier).unwrap_err(),
            InputError::Registry
        );
    }
}
