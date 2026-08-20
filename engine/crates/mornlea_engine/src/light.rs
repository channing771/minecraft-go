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
        // 最小可能扣减是 1，所以 current<=1 时任何邻居都拿不到正的光照。
        if current <= 1 {
            continue;
        }
        // best 是本格能给出的**最好**结果（扣减恰好为 1）。先拿它剪枝，已经不低于
        // best 的邻居连查表都不必做——这既省掉热路径上的两次二分查找，也保住了
        // 「稳定输入不再采样已定型邻居」这条既有性质。
        let best = current - 1;
        for (dx, dy, dz) in DIRECTIONS {
            let (nx, ny, nz) = (x + dx, y + dy, z + dz);
            if !inside(nx, ny, nz) {
                continue;
            }
            let next = light_index(nx, ny, nz);
            if scratch.levels[next] >> 4 >= best {
                continue;
            }
            let id = input.block(nx, ny, nz);
            if registry.opaque(id) {
                continue;
            }
            // 每格扣减 = 固定的 1 + 目标方块查表得到的额外衰减。六个方向共用同一个
            // 公式，竖直向下没有任何特例：水的额外衰减是 1，于是每下沉一格扣 2。
            let step = 1 + registry.light_attenuation(id);
            if current <= step {
                continue;
            }
            let candidate = current - step;
            if scratch.levels[next] >> 4 >= candidate {
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
    const ENTRY_BYTES: usize = crate::input::tests::ENTRY_BYTES;
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

    #[test]
    fn reused_scratch_clears_old_light_before_a_dark_build() {
        let lit = fixture_with_light_block(8, 8, 8);
        let dark = parse_fixture(base_input(0));
        let mut storage = ScratchFixture::new();

        build_light(&lit.mesh, &lit.mesh.registry, &mut storage.light).unwrap();
        assert_eq!(storage.light.at(8, 8, 8) & 0x0f, 15);
        build_light(&dark.mesh, &dark.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(8, 8, 8), 0);
        assert_eq!(storage.light.at(9, 8, 8), 0);
    }

    #[test]
    fn non_air_non_opaque_block_stops_block_light() {
        let mut bytes = base_input(15);
        for block in bytes[BLOCKS_OFFSET..BLOCKS_OFFSET + BLOCKS_BYTES].chunks_exact_mut(2) {
            block.copy_from_slice(&1_u16.to_le_bytes());
        }
        bytes[REGISTRY_OFFSET + ENTRY_BYTES + 2] = 0;
        set_block(&mut bytes, 8, 8, 8, LIGHT_ID);
        set_block(&mut bytes, 10, 8, 8, 0);
        let input = parse_fixture(bytes);
        let mut storage = ScratchFixture::new();

        build_light(&input.mesh, &input.mesh.registry, &mut storage.light).unwrap();

        assert_eq!(storage.light.at(9, 8, 8) & 0x0f, 0);
        assert_eq!(storage.light.at(10, 8, 8) & 0x0f, 0);
    }

    /// 天空光穿过流体时每格额外衰减：固定的 1 加上查表得到的 `light_attenuation`。
    ///
    /// 水的 `light_attenuation = 1`，所以每下沉一格扣 2：15 → 13 → 11 → … → 1 → 0。
    /// **竖直向下不再无损**：流体格即使严格高于列顶也不再是直射起点，光只能从上方的
    /// 空气逐格付费进入。
    #[test]
    fn sky_light_dims_by_two_per_fluid_cell() {
        let water = open_water_fixture(LIGHT_ID);
        // 夹具把 LIGHT_ID 当水用，靠的是 valid_input() 给它写了 light_attenuation=1
        // 这一**隐式**事实（base_input 显式清零了 fluid_height 那个字节，却没碰它）。
        // 显式钉住，免得夹具改动悄悄把「水」变成普通透明方块、整组断言退化。
        assert_eq!(water.mesh.registry.light_attenuation(LIGHT_ID), 1);
        let mut storage = ScratchFixture::new();
        build_light(&water.mesh, &water.mesh.registry, &mut storage.light).unwrap();

        // 水面之上的空气仍然是满亮直射起点。
        assert_eq!(storage.light.at(8, 8, 8) >> 4, 15);
        // 紧邻水面之下必须大于 0，且逐格严格变暗，足够深处到 0。
        let want = [13, 11, 9, 7, 5, 3, 1, 0];
        for (depth, expected) in want.into_iter().enumerate() {
            let y = 7 - depth as i32;
            assert_eq!(
                storage.light.at(8, y, 8) >> 4,
                expected,
                "水下第 {} 格（y={y}）的天空光不符",
                depth + 1,
            );
        }

        // 防空转守卫排在真实断言之后：把水换成空气后同样八格必须全是 15。
        // 若对照组也变暗，说明变暗来自夹具被封顶之类的原因，而不是流体衰减，
        // 上面那串读数就不再证明任何事。
        let air = open_water_fixture(0);
        let mut control = ScratchFixture::new();
        build_light(&air.mesh, &air.mesh.registry, &mut control.light).unwrap();
        for y in 0..8 {
            assert_eq!(
                control.light.at(8, y, 8) >> 4,
                15,
                "空气对照组 y={y} 不是满亮，夹具本身就是暗的"
            );
        }
    }

    /// 流体透光：水下的光来自 BFS 逐格付费，而不是「被流体完全阻断」。
    /// 与 `opaque_cell_blocks_sky_propagation` 成对——不透明格是 0，流体格不是。
    #[test]
    fn fluid_attenuates_instead_of_blocking() {
        let water = open_water_fixture(LIGHT_ID);
        let mut storage = ScratchFixture::new();
        build_light(&water.mesh, &water.mesh.registry, &mut storage.light).unwrap();

        assert!(storage.light.at(8, 7, 8) >> 4 > 0);
        assert!(storage.light.at(8, 7, 8) >> 4 < 15);
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
        bytes[REGISTRY_OFFSET + 2 * ENTRY_BYTES + 3] = emission;
        bytes[REGISTRY_OFFSET + 2 * ENTRY_BYTES + 16] = 0;
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
        fill_height_section(bytes, x, z, 31);
    }

    /// fill_height_section 把 (x,z) 所在整个 16×16 区段列的列顶统一设成 highest。
    fn fill_height_section(bytes: &mut [u8], x: i32, z: i32, highest: i16) {
        let (cx, _) = neighbor_cell(x);
        let (cz, _) = neighbor_cell(z);
        let column = cx * 3 + cz;
        bytes[HEIGHTS_PRESENT_OFFSET + column] = 1;
        for cell in 0..256 {
            let offset = HEIGHTS_OFFSET + (column * 256 + cell) * 2;
            bytes[offset..offset + 2].copy_from_slice(&highest.to_le_bytes());
        }
    }

    /// open_water_fixture 造一片**露天水体**：`fill` 铺满中心 16×16 区段列的
    /// y=0..=7 八格，其上是空气，没有任何遮挡。
    ///
    /// `fill` 传 `LIGHT_ID` 时就是水：在 `base_input(0)` 下它正好是一块「非不透明、
    /// 不发光、`light_attenuation = 1`」的方块。传 `0`（空气）就是同一夹具的对照组。
    ///
    /// 列顶按 `fill` 派生，与 `world.Chunk` 的 `updateHeight` 同口径：水是非空气方块，
    /// 会把列顶抬到水面 `y=7`（于是水格自身 `sky_light=0`、只能靠 BFS 进光）；空气
    /// 对照组则是空列（-17，低于整个光照体积）。
    ///
    /// 夹具刻意做成整段 16×16 满铺八格深：**单列水柱是测不出衰减的**——旁边的空气
    /// 会以每格 1 的代价把光送到同样深度再横向灌进来，读数被空气路径而不是水路径
    /// 决定。满铺之后中心列 (8,·,8) 的每个横向邻居都是同深度的水，唯一路径是竖直
    /// 穿水。八格深也是刻意的：一格深时「衰减 1」与「衰减 0」会给出同一批读数。
    fn open_water_fixture(fill: u16) -> InputFixture {
        let mut bytes = base_input(0);
        let highest = if fill == 0 { -17 } else { 7 };
        fill_height_section(&mut bytes, 8, 8, highest);
        for x in 0..16 {
            for z in 0..16 {
                for y in 0..8 {
                    set_block(&mut bytes, x, y, z, fill);
                }
            }
        }
        parse_fixture(bytes)
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
