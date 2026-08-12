use crate::input::{MeshInput, RegistryView};

pub(crate) const LIGHT_MIN: i32 = -16;
pub(crate) const LIGHT_SIDE: usize = 48;
pub(crate) const LIGHT_VOLUME: usize = LIGHT_SIDE * LIGHT_SIDE * LIGHT_SIDE;
const SKY_MASK: u8 = 0xf0;
const BLOCK_MASK: u8 = 0x0f;
const DIRECTIONS: [(i32, i32, i32); 6] = [
    (-1, 0, 0),
    (1, 0, 0),
    (0, -1, 0),
    (0, 1, 0),
    (0, 0, -1),
    (0, 0, 1),
];

#[derive(Debug, PartialEq, Eq)]
pub(crate) enum MeshError {
    EmissionOutOfRange,
    QueueOverflow,
}

pub(crate) struct LightScratch<'a> {
    levels: &'a mut [u8],
    queue: &'a mut [u32],
    head: usize,
    tail: usize,
}

impl<'a> LightScratch<'a> {
    pub(crate) fn new(levels: &'a mut [u8], queue: &'a mut [u32]) -> Self {
        Self {
            levels,
            queue,
            head: 0,
            tail: 0,
        }
    }

    pub(crate) fn at(&self, x: i32, y: i32, z: i32) -> u8 {
        if !inside(x, y, z) {
            return 0;
        }
        self.levels[light_index(x, y, z)]
    }

    fn enqueue(&mut self, index: usize) -> Result<(), MeshError> {
        if self.tail == self.queue.len() {
            return Err(MeshError::QueueOverflow);
        }
        self.queue[self.tail] = index as u32;
        self.tail += 1;
        Ok(())
    }

    fn reset_queue(&mut self) {
        self.head = 0;
        self.tail = 0;
    }

    #[cfg(test)]
    fn tail(&self) -> usize {
        self.tail
    }
}

pub(crate) fn build_light(
    input: &MeshInput<'_>,
    registry: &RegistryView<'_>,
    scratch: &mut LightScratch<'_>,
) -> Result<(), MeshError> {
    scratch.levels.fill(0);
    scratch.reset_queue();
    build_sky(input, registry, scratch)?;
    scratch.reset_queue();
    build_block(input, registry, scratch)
}

fn build_sky(
    input: &MeshInput<'_>,
    registry: &RegistryView<'_>,
    scratch: &mut LightScratch<'_>,
) -> Result<(), MeshError> {
    let end = LIGHT_MIN + LIGHT_SIDE as i32;
    for x in LIGHT_MIN..end {
        for y in LIGHT_MIN..end {
            for z in LIGHT_MIN..end {
                if input.sky_light(x, y, z) != 15 || registry.opaque(input.block(x, y, z)) {
                    continue;
                }
                let index = light_index(x, y, z);
                scratch.levels[index] = SKY_MASK;
                scratch.enqueue(index)?;
            }
        }
    }

    while scratch.head < scratch.tail {
        let mut index = scratch.queue[scratch.head] as usize;
        scratch.head += 1;
        let z = (index % LIGHT_SIDE) as i32 + LIGHT_MIN;
        index /= LIGHT_SIDE;
        let y = (index % LIGHT_SIDE) as i32 + LIGHT_MIN;
        let x = (index / LIGHT_SIDE) as i32 + LIGHT_MIN;
        let current = scratch.at(x, y, z) >> 4;
        if current <= 1 {
            continue;
        }
        let candidate = current - 1;
        for (dx, dy, dz) in DIRECTIONS {
            let (nx, ny, nz) = (x + dx, y + dy, z + dz);
            if !inside(nx, ny, nz) {
                continue;
            }
            let next = light_index(nx, ny, nz);
            if scratch.levels[next] >> 4 >= candidate || registry.opaque(input.block(nx, ny, nz)) {
                continue;
            }
            scratch.levels[next] = (scratch.levels[next] & BLOCK_MASK) | (candidate << 4);
            scratch.enqueue(next)?;
        }
    }
    Ok(())
}

fn build_block(
    input: &MeshInput<'_>,
    registry: &RegistryView<'_>,
    scratch: &mut LightScratch<'_>,
) -> Result<(), MeshError> {
    let end = LIGHT_MIN + LIGHT_SIDE as i32;
    for x in LIGHT_MIN..end {
        for y in LIGHT_MIN..end {
            for z in LIGHT_MIN..end {
                let level = registry.emission(input.block(x, y, z));
                if level == 0 {
                    continue;
                }
                if level > 15 {
                    return Err(MeshError::EmissionOutOfRange);
                }
                let index = light_index(x, y, z);
                scratch.levels[index] = (scratch.levels[index] & SKY_MASK) | level;
                scratch.enqueue(index)?;
            }
        }
    }

    while scratch.head < scratch.tail {
        let mut index = scratch.queue[scratch.head] as usize;
        scratch.head += 1;
        let z = (index % LIGHT_SIDE) as i32 + LIGHT_MIN;
        index /= LIGHT_SIDE;
        let y = (index % LIGHT_SIDE) as i32 + LIGHT_MIN;
        let x = (index / LIGHT_SIDE) as i32 + LIGHT_MIN;
        let current = scratch.at(x, y, z) & BLOCK_MASK;
        if current <= 1 {
            continue;
        }
        let candidate = current - 1;
        for (dx, dy, dz) in DIRECTIONS {
            let (nx, ny, nz) = (x + dx, y + dy, z + dz);
            if !inside(nx, ny, nz) {
                continue;
            }
            let next = light_index(nx, ny, nz);
            if scratch.levels[next] & BLOCK_MASK >= candidate
                || input.block(nx, ny, nz) != input.air_id
            {
                continue;
            }
            scratch.levels[next] = (scratch.levels[next] & SKY_MASK) | candidate;
            scratch.enqueue(next)?;
        }
    }
    Ok(())
}

fn inside(x: i32, y: i32, z: i32) -> bool {
    let end = LIGHT_MIN + LIGHT_SIDE as i32;
    (LIGHT_MIN..end).contains(&x) && (LIGHT_MIN..end).contains(&y) && (LIGHT_MIN..end).contains(&z)
}

fn light_index(x: i32, y: i32, z: i32) -> usize {
    (((x - LIGHT_MIN) as usize * LIGHT_SIDE + (y - LIGHT_MIN) as usize) * LIGHT_SIDE)
        + (z - LIGHT_MIN) as usize
}

#[cfg(test)]
mod tests {
    use super::{LIGHT_VOLUME, LightScratch, MeshError, build_light};
    use crate::input::{MeshInput, tests::valid_input};

    const BLOCKS_OFFSET: usize = 16;
    const BLOCKS_BYTES: usize = 27 * 4096 * 2;
    const HEIGHTS_PRESENT_OFFSET: usize = BLOCKS_OFFSET + BLOCKS_BYTES;
    const HEIGHTS_OFFSET: usize = HEIGHTS_PRESENT_OFFSET + 9;
    const REGISTRY_OFFSET: usize = HEIGHTS_OFFSET + 9 * 256 * 2;
    const LIGHT_ID: u16 = 40000;

    struct InputFixture {
        mesh: MeshInput<'static>,
    }

    struct ScratchFixture {
        light: LightScratch<'static>,
    }

    impl ScratchFixture {
        fn new() -> Self {
            let levels = Box::leak(vec![0; LIGHT_VOLUME].into_boxed_slice());
            let queue = Box::leak(vec![0; LIGHT_VOLUME].into_boxed_slice());
            Self {
                light: LightScratch::new(levels, queue),
            }
        }
    }

    #[test]
    fn single_block_light_reaches_fourteen_next_door() {
        let input = fixture_with_light_block(8, 8, 8);
        let mut storage = ScratchFixture::new();
        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();
        assert_eq!(storage.light.at(8, 8, 8) & 0x0f, 15);
        assert_eq!(storage.light.at(9, 8, 8) & 0x0f, 14);
    }

    #[test]
    fn all_sources_fill_exact_queue_without_overflow() {
        let input = fixture_all_light_blocks();
        let mut storage = ScratchFixture::new();
        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();
        assert_eq!(storage.light.tail(), LIGHT_VOLUME);
    }

    #[test]
    fn one_short_queue_reports_overflow() {
        let input = fixture_all_light_blocks();
        let levels = Box::leak(vec![0; LIGHT_VOLUME].into_boxed_slice());
        let queue = Box::leak(vec![0; LIGHT_VOLUME - 1].into_boxed_slice());
        let mut light = LightScratch::new(levels, queue);

        assert_eq!(
            build_light(&input.mesh, &input.mesh.registry, &mut light),
            Err(MeshError::QueueOverflow),
        );
    }

    #[test]
    fn emission_sixteen_is_rejected() {
        let input = fixture_with_emission(16);
        let mut storage = ScratchFixture::new();
        assert_eq!(
            build_light(&input.mesh, &input.mesh.registry, &mut storage.light),
            Err(MeshError::EmissionOutOfRange),
        );
    }

    #[test]
    fn direct_sky_seed_is_fifteen() {
        let input = fixture_with_sky_column(8, 8, 7);
        let mut storage = ScratchFixture::new();

        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(8, 8, 8) >> 4, 15);
    }

    #[test]
    fn sky_light_attenuates_to_fourteen_next_door() {
        let input = fixture_with_sky_column(8, 8, 7);
        let mut storage = ScratchFixture::new();

        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(9, 8, 8) >> 4, 14);
    }

    #[test]
    fn opaque_cell_blocks_sky_propagation() {
        let mut bytes = base_input(0);
        set_height(&mut bytes, 8, 8, Some(7));
        set_block(&mut bytes, 9, 8, 8, 1);
        let input = parse_fixture(bytes);
        let mut storage = ScratchFixture::new();

        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(9, 8, 8) >> 4, 0);
    }

    #[test]
    fn missing_height_stays_dark() {
        let input = parse_fixture(base_input(0));
        let mut storage = ScratchFixture::new();

        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(8, 8, 8) >> 4, 0);
    }

    #[test]
    fn queue_is_reused_between_sky_and_block_passes() {
        let mut bytes = base_input(15);
        for x in -16..32 {
            for z in -16..32 {
                set_height(&mut bytes, x, z, Some(-17));
            }
        }
        set_block(&mut bytes, 8, 8, 8, LIGHT_ID);
        let input = parse_fixture(bytes);
        let mut storage = ScratchFixture::new();

        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.tail(), 4089);
        assert_eq!(storage.light.at(9, 8, 8) & 0x0f, 14);
    }

    #[test]
    fn outside_light_volume_returns_zero() {
        let input = fixture_with_light_block(8, 8, 8);
        let mut storage = ScratchFixture::new();
        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(-17, 0, 0), 0);
        assert_eq!(storage.light.at(32, 0, 0), 0);
        assert_eq!(storage.light.at(0, -17, 0), 0);
        assert_eq!(storage.light.at(0, 32, 0), 0);
        assert_eq!(storage.light.at(0, 0, -17), 0);
        assert_eq!(storage.light.at(0, 0, 32), 0);
    }

    fn fixture_with_light_block(x: i32, y: i32, z: i32) -> InputFixture {
        let mut bytes = base_input(15);
        set_block(&mut bytes, x, y, z, LIGHT_ID);
        parse_fixture(bytes)
    }

    fn fixture_all_light_blocks() -> InputFixture {
        let mut bytes = base_input(15);
        for block in bytes[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].chunks_exact_mut(2) {
            block.copy_from_slice(&LIGHT_ID.to_le_bytes());
        }
        parse_fixture(bytes)
    }

    fn fixture_with_emission(emission: u8) -> InputFixture {
        let mut bytes = base_input(emission);
        set_block(&mut bytes, 8, 8, 8, LIGHT_ID);
        let bytes = Box::leak(bytes.into_boxed_slice());
        InputFixture {
            mesh: MeshInput::parse_allowing_overbright(bytes).unwrap(),
        }
    }

    fn fixture_with_sky_column(x: i32, z: i32, highest: i16) -> InputFixture {
        let mut bytes = base_input(0);
        darken_height_section(&mut bytes, x, z);
        set_height(&mut bytes, x, z, Some(highest));
        parse_fixture(bytes)
    }

    fn base_input(emission: u8) -> Vec<u8> {
        let mut bytes = valid_input();
        bytes[4..8].copy_from_slice(&0_i32.to_le_bytes());
        bytes[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].fill(0);
        bytes[HEIGHTS_PRESENT_OFFSET..HEIGHTS_PRESENT_OFFSET + 9].fill(0);
        bytes[HEIGHTS_OFFSET..REGISTRY_OFFSET].fill(0);
        bytes[REGISTRY_OFFSET + 2 * 16 + 3] = emission;
        bytes
    }

    fn set_block(bytes: &mut [u8], x: i32, y: i32, z: i32, id: u16) {
        let (cx, lx) = neighbor_cell(x);
        let (cy, ly) = neighbor_cell(y);
        let (cz, lz) = neighbor_cell(z);
        let section = (cx * 3 + cy) * 3 + cz;
        let cell = (ly << 8) | (lz << 4) | lx;
        let offset = BLOCKS_OFFSET + (section * 4096 + cell) * 2;
        bytes[offset..offset + 2].copy_from_slice(&id.to_le_bytes());
    }

    fn set_height(bytes: &mut [u8], x: i32, z: i32, highest: Option<i16>) {
        let (cx, lx) = neighbor_cell(x);
        let (cz, lz) = neighbor_cell(z);
        let column = cx * 3 + cz;
        bytes[HEIGHTS_PRESENT_OFFSET + column] = u8::from(highest.is_some());
        let offset = HEIGHTS_OFFSET + (column * 256 + (lz << 4) + lx) * 2;
        bytes[offset..offset + 2].copy_from_slice(&highest.unwrap_or_default().to_le_bytes());
    }

    fn darken_height_section(bytes: &mut [u8], x: i32, z: i32) {
        let (cx, _) = neighbor_cell(x);
        let (cz, _) = neighbor_cell(z);
        let column = cx * 3 + cz;
        bytes[HEIGHTS_PRESENT_OFFSET + column] = 1;
        for cell in 0..256 {
            let offset = HEIGHTS_OFFSET + (column * 256 + cell) * 2;
            bytes[offset..offset + 2].copy_from_slice(&31_i16.to_le_bytes());
        }
    }

    fn neighbor_cell(value: i32) -> (usize, usize) {
        let shifted = value + 16;
        ((shifted >> 4) as usize, (shifted & 15) as usize)
    }

    fn parse_fixture(bytes: Vec<u8>) -> InputFixture {
        let bytes = Box::leak(bytes.into_boxed_slice());
        InputFixture {
            mesh: MeshInput::parse(bytes).unwrap(),
        }
    }
}
