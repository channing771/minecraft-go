//! water pass 的离屏对照测试（无窗口，走既有 `OffscreenRenderer` + `readback`）。
//!
//! 全部用例共用一台**正交俯视**相机：`clip.x`/`clip.y` 只由世界 `x`/`z` 决定，
//! `clip.z` 只由世界 `y` 决定。于是「一个方块 = 屏幕上一块 16×16 像素的方格」，
//! 每个方格互不干扰，可以在一帧里同时摆好多个互相对照的场景，而深度关系完全
//! 由方块高度决定、可以手算。
//!
//! 三种材质层固定为纯色，便于逐通道断言：
//! 0 = 不透明红、1 = 半透明蓝（水）、2 = 不透明绿。

use super::*;

/// PosY（朝上的顶面）的 face 编号，与 `quad.rs` 的 `Face::PosY` 一致。
const FACE_POS_Y: u32 = 3;
/// 水材质层在测试 atlas 中的层号。
const MAT_WATER: u16 = 1;
/// 不透明红 / 绿两层。
const MAT_RED: u16 = 0;
const MAT_GREEN: u16 = 2;
/// 满格角高度原值（实际高度 (15+1)/16 = 1）。
const FULL: [u8; 4] = [15; 4];
/// 一个 atlas 层（含全部 mip）的字节数：16²+8²+4²+2²+1² 个 RGBA 像素。
const ATLAS_LAYER_BYTES: usize = (256 + 64 + 16 + 4 + 1) * 4;
/// 离屏画面边长。
const VIEW: u32 = 64;

/// pack_quad 复刻 `mornlea_engine::quad::Quad::pack` 的位布局。
///
/// 这里**必须**手写一份而不是复用 engine：client crate 不依赖 engine，而位
/// 布局本身就是两个 crate 之间的契约。任一侧改位而不改另一侧，这些用例会以
/// 图像不符的形式报出来。
#[allow(clippy::too_many_arguments)]
fn pack_quad(
    x: u8,
    y: u8,
    z: u8,
    w: u8,
    h: u8,
    face: u32,
    material: u16,
    ao: u8,
    light: u8,
    corners: [u8; 4],
) -> u64 {
    let low = if corners == [0; 4] {
        u64::from(w - 1) << 12 | u64::from(h - 1) << 16
    } else {
        assert!(w == 1 && h == 1, "带角高度的 quad 必须是 1×1");
        u64::from(corners[0]) << 12 | u64::from(corners[1]) << 16
    };
    let high = u64::from(corners[2]) << 55 | u64::from(corners[3]) << 59;
    u64::from(x)
        | u64::from(y) << 4
        | u64::from(z) << 8
        | low
        | u64::from(face) << 20
        | u64::from(material) << 23
        | u64::from(ao) << 39
        | u64::from(light) << 47
        | high
}

/// 一格地面：世界 y = 1 的一块 1×1 顶面。
fn floor_cell(bx: u8, bz: u8, material: u16) -> u64 {
    pack_quad(bx, 0, bz, 1, 1, FACE_POS_Y, material, 0xFF, 0xFF, [0; 4])
}

/// 一格「压在头顶的不透明方块顶面」：世界 y = 11。
fn lid_cell(bx: u8, bz: u8) -> u64 {
    pack_quad(bx, 10, bz, 1, 1, FACE_POS_Y, MAT_RED, 0xFF, 0xFF, [0; 4])
}

/// 一格满格水面，顶面世界 y = `local_y + 1`。
fn water_cell(bx: u8, local_y: u8, bz: u8, light: u8) -> u64 {
    pack_quad(
        bx, local_y, bz, 1, 1, FACE_POS_Y, MAT_WATER, 0xFF, light, FULL,
    )
}

/// 正交俯视 view_proj（列主序，与 WGSL 的 mat4x4f 内存布局一致）：
///
/// ```text
/// clip.x = world.x / 2 - 4        （世界 x ∈ [6,10] 落在 [-1,1]）
/// clip.y = world.z / 2 - 4
/// clip.z = 0.5 - world.y / 200    （y 越高越近，全程落在 (0,1)）
/// ```
fn top_down_view_proj() -> [f32; 16] {
    [
        0.5, 0.0, 0.0, 0.0, // 第 0 列
        0.0, 0.0, -0.005, 0.0, // 第 1 列
        0.0, 0.5, 0.0, 0.0, // 第 2 列
        -4.0, -4.0, 0.5, 1.0, // 第 3 列
    ]
}

/// 斜视 view_proj：在俯视基础上把世界 `y` 掺进 `clip.y`，于是**角高度直接
/// 体现为屏幕行号**——这是唯一能不靠深度就看见角高度的投影。
///
/// ```text
/// clip.x = world.x * 0.25 - 2
/// clip.y = world.y * 0.15 + world.z * 0.25 - 3.2
/// clip.z = 0.5 - world.y / 200
/// ```
///
/// 角高度原值差 8（15 → 7）对应世界高度差 0.5，即 `clip.y` 差 0.075，
/// 在 64 像素高的画面上是 `0.075 / 2 * 64 = 2.4` 行。
fn oblique_view_proj() -> [f32; 16] {
    [
        0.25, 0.0, 0.0, 0.0, // 第 0 列
        0.0, 0.15, -0.005, 0.0, // 第 1 列
        0.0, 0.25, 0.0, 0.0, // 第 2 列
        -2.0, -3.2, 0.5, 1.0, // 第 3 列
    ]
}

/// 生成 `colors.len()` 层纯色 atlas（逐层逐 mip，与 Go `AtlasPixels` 同布局）。
fn atlas_bytes(colors: &[[u8; 4]]) -> Vec<u8> {
    let mut out = Vec::with_capacity(colors.len() * ATLAS_LAYER_BYTES);
    for color in colors {
        for mip in 0..ATLAS_MIPS {
            let size = (ATLAS_TEX_SIZE >> mip).max(1) as usize;
            for _ in 0..size * size {
                out.extend_from_slice(color);
            }
        }
    }
    out
}

/// u64 quad 序列 → 上传字节（小端，8 字节/条）。
fn quad_bytes(quads: &[u64]) -> Vec<u8> {
    let mut out = Vec::with_capacity(quads.len() * 8);
    for quad in quads {
        out.extend_from_slice(&quad.to_le_bytes());
    }
    out
}

/// 一个区段的两条流。
struct SectionData {
    pos: (i32, i32, i32),
    opaque: Vec<u64>,
    water: Vec<u64>,
}

/// 建渲染器、上传 atlas 与区段、渲染**一帧**并回读 BGRA 图像。
///
/// 每次都新建渲染器：首帧 `have_last_camera` 为假，HiZ 必然停用，图像因此只
/// 取决于本用例的输入。无 GPU 适配器时返回 None（调用方跳过，与既有约定一致）。
fn render_once(view_proj: [f32; 16], sections: &[SectionData]) -> Option<Vec<u8>> {
    let mut renderer = OffscreenRenderer::new(VIEW, VIEW).ok()?;
    let colors = [
        [200u8, 60, 60, 255], // 层 0：不透明红
        [40u8, 90, 200, 160], // 层 1：半透明蓝（水）
        [60u8, 200, 60, 255], // 层 2：不透明绿
    ];
    assert!(renderer.upload_atlas(colors.len() as u32, &atlas_bytes(&colors)));
    let mut visible = Vec::new();
    for section in sections {
        assert!(renderer.upload_section(
            section.pos,
            &quad_bytes(&section.opaque),
            &quad_bytes(&section.water),
        ));
        visible.push(section.pos);
    }
    let mut identity = [0.0f32; 16];
    for i in 0..4 {
        identity[i * 4 + i] = 1.0;
    }
    let frame = FrameInput {
        view_proj,
        view_proj_inv: identity,
        pos: [8.0, 100.0, 8.0],
        daylight: 1.0,
        sun_direction: [0.0, 1.0, 0.0],
        star_visibility: 0.0,
        sky_color: [0.25, 0.5, 1.0, 1.0],
        cloud_macro_x: 0,
        cloud_local: 0.0,
        visible,
        ..Default::default()
    };
    assert_eq!(renderer.render_frame(&frame), FrameResult::Rendered);
    let mut image = vec![0u8; (VIEW * VIEW * 4) as usize];
    assert!(renderer.readback(&mut image));
    Some(image)
}

/// 取一个像素，返回 `[r, g, b]`（回读是 BGRA）。
fn pixel(image: &[u8], x: u32, row: u32) -> [u8; 3] {
    let i = ((row * VIEW + x) * 4) as usize;
    [image[i + 2], image[i + 1], image[i]]
}

/// 俯视相机下方块 `(bx, bz)` 的中心像素。
fn cell_pixel(image: &[u8], bx: u32, bz: u32) -> [u8; 3] {
    // clip.x = (bx + 0.5) / 2 - 4，屏幕 x = (clip.x + 1) / 2 * 64。
    let x = (((bx as f32 + 0.5) / 2.0 - 4.0) + 1.0) / 2.0 * VIEW as f32;
    let clip_y = (bz as f32 + 0.5) / 2.0 - 4.0;
    let row = (1.0 - clip_y) / 2.0 * VIEW as f32;
    pixel(image, x as u32, row as u32)
}

/// 六个互相对照的方格摆在同一帧里；返回图像。方格布局见各用例的断言。
///
/// 关键的上传顺序：`(7,7)` 的两片水面**近的先传**。区段内不排序（排序粒度止于
/// 区段），因此这个顺序就是绘制顺序——深度写一旦打开，后画的远水面会被近水面
/// 挡掉，正是本组要防的回归。
fn comparison_scene() -> Option<Vec<u8>> {
    let opaque = vec![
        floor_cell(6, 6, MAT_RED),
        floor_cell(7, 6, MAT_RED),
        floor_cell(8, 6, MAT_GREEN),
        floor_cell(9, 6, MAT_RED),
        floor_cell(6, 7, MAT_RED),
        floor_cell(7, 7, MAT_RED),
        floor_cell(8, 7, MAT_RED),
        floor_cell(9, 7, MAT_RED),
        lid_cell(9, 6),
        lid_cell(6, 7),
    ];
    let water = vec![
        water_cell(7, 8, 6, 0xFF),
        water_cell(8, 8, 6, 0xFF),
        water_cell(9, 2, 6, 0xFF),
        water_cell(7, 8, 7, 0xFF),
        water_cell(7, 2, 7, 0xFF),
    ];
    render_once(
        top_down_view_proj(),
        &[SectionData {
            pos: (0, 4, 0),
            opaque,
            water,
        }],
    )
}

/// Scenario「水面不遮挡其后的水面」。
///
/// `(7,7)` 是前后两片水面，`(7,6)` 是同样地面上的一片水面。远的那片若被近的
/// 裁掉，两格会变得完全相同。这里断言的是**方向**：叠了两层水之后蓝色更强、
/// 红色更弱——单看「不相等」会被任何噪声满足，方向断言不会。
#[test]
fn far_water_surface_is_not_culled_by_the_nearer_one() {
    let Some(image) = comparison_scene() else {
        return;
    };
    let one = cell_pixel(&image, 7, 6);
    let two = cell_pixel(&image, 7, 7);
    assert!(
        two[2] > one[2],
        "两层水面的蓝色应强于一层：two={two:?} one={one:?}"
    );
    assert!(
        two[0] < one[0],
        "两层水面的红色应弱于一层：two={two:?} one={one:?}"
    );
}

/// Scenario「水面被不透明方块正确遮挡」。
///
/// `(9,6)` 的水面在世界 y=3，头顶压着 y=11 的不透明方块；`(6,7)` 只有那块
/// 不透明方块。被遮挡的水面必须**一个像素都不出现**，两格因此逐字节相同。
#[test]
fn water_behind_an_opaque_block_is_fully_hidden() {
    let Some(image) = comparison_scene() else {
        return;
    };
    let occluded = cell_pixel(&image, 9, 6);
    let bare = cell_pixel(&image, 6, 7);
    assert_eq!(
        occluded, bare,
        "不透明方块之后的水面不得出现在画面上：occluded={occluded:?} bare={bare:?}"
    );
}

/// Scenario「水面之下的地形可见」。
///
/// `(7,6)` 与 `(8,6)` 的水面完全相同，只有水底地形一红一绿。若水底不可见，
/// 两格必然相同——它们不同，就是「透过水面看见地形」的直接证据。
/// 随后再断言水色可辨识：相对裸地面，蓝色升、红色降。
#[test]
fn terrain_under_water_stays_visible_and_water_is_tinted() {
    let Some(image) = comparison_scene() else {
        return;
    };
    let over_red = cell_pixel(&image, 7, 6);
    let over_green = cell_pixel(&image, 8, 6);
    assert_ne!(
        over_red, over_green,
        "水底地形必须透过水面可见：over_red={over_red:?} over_green={over_green:?}"
    );
    let bare = cell_pixel(&image, 6, 6);
    assert!(
        over_red[2] > bare[2] && over_red[0] < bare[0],
        "水面应对水底地形呈现可辨识水色：over_red={over_red:?} bare={bare:?}"
    );
}

/// 水面只覆盖它自己那一格。
///
/// 这条直指当前中间态的破损点：水面 quad 的 bit 12..19 装的是角高度 7..15，
/// 若着色器沿用 terrain 的 `w-1`/`h-1` 解码，一片水面会摊成 8×8 到 16×16 的
/// 巨型石板，把邻格的裸地面一并盖住。裸地面 `(6,6)` 必须仍是红色主导。
#[test]
fn water_surface_covers_only_its_own_cell() {
    let Some(image) = comparison_scene() else {
        return;
    };
    // (8,7) 与 (9,7) 特意摆在「巨型石板」的覆盖范围内：水面 quad 一旦按
    // w/h 展开成 16×16，它们会被 (7,6) 那片水盖住。
    for (bx, bz) in [(6u32, 6u32), (8, 7), (9, 7)] {
        let bare = cell_pixel(&image, bx, bz);
        assert!(
            bare[0] > bare[2],
            "裸地面格 ({bx},{bz}) 必须保持红色主导（未被邻格水面覆盖）：bare={bare:?}"
        );
    }
}

/// 角高度必须真的抬高/压低水面顶点，且幅度对得上位布局。
///
/// 斜视投影把世界 `y` 掺进 `clip.y`，于是角高度直接体现为水面上边缘的屏幕行号。
/// 角高度原值 15 → 7 对应世界高度降 0.5，即 `0.075 / 2 * 64 = 2.4` 行。断言
/// 位移落在 1..=4 行：
///
/// - 完全不解码角高度（顶点恒在 y+1）→ 位移 0，红；
/// - 沿用 `w-1`/`h-1` 解码 → 面变成 16×16 与 8×8，上边缘位移约 64 行，红。
#[test]
fn corner_heights_move_the_water_surface_vertically() {
    let baseline = SectionData {
        pos: (0, 4, 0),
        opaque: vec![],
        water: vec![],
    };
    let Some(empty) = render_once(oblique_view_proj(), std::slice::from_ref(&baseline)) else {
        return;
    };
    let mut top_rows = Vec::new();
    for raw in [15u8, 7u8] {
        let quad = pack_quad(8, 10, 8, 1, 1, FACE_POS_Y, MAT_WATER, 0xFF, 0xFF, [raw; 4]);
        let image = render_once(
            oblique_view_proj(),
            &[SectionData {
                pos: (0, 4, 0),
                opaque: vec![],
                water: vec![quad],
            }],
        )
        .expect("首个场景已成功建过渲染器");
        let top = (0..VIEW)
            .find(|&row| {
                (0..VIEW).any(|x| {
                    let (a, b) = (pixel(&image, x, row), pixel(&empty, x, row));
                    (0..3).any(|c| a[c].abs_diff(b[c]) >= 8)
                })
            })
            .unwrap_or_else(|| panic!("角高度 {raw} 的水面完全没有出现在画面上"));
        top_rows.push(top);
    }
    let (full, half) = (top_rows[0], top_rows[1]);
    assert!(
        half > full,
        "角高度更低的水面必须更靠下（行号更大）：full={full} half={half}"
    );
    let shift = half - full;
    assert!(
        (1..=4).contains(&shift),
        "角高度 15 → 7 的位移应约 2.4 行，实测 {shift} 行（full={full} half={half}）"
    );
}

/// Scenario「排序粒度不细于区段」+「按由远及近绘制」。
///
/// 两条断言方向相反，合起来正好把排序粒度夹在「区段」这一档：
///
///  1. **同一区段内不排序**：两片深浅不同的水面前后叠放，只交换它们在上传流里
///     的顺序，画面就必须变——alpha blend 不可交换。若实现对区段内的单个面按
///     距离排序，两次绘制顺序会被归一化成同一个，画面反而相同。
///  2. **跨区段按距离排序**：同样两片水面拆进两个区段，只交换 `visible` 列表的
///     顺序，画面必须**不变**——绘制顺序由距离决定，与调用方给的次序无关。
///
/// 第 1 条同时是第 2 条的非空转证据：它证明这两片水面的绘制顺序确实会改变画面。
#[test]
fn sorting_granularity_is_the_section() {
    let bright = water_cell(8, 10, 8, 0xFF);
    let dim = water_cell(8, 2, 8, 0x88);
    let same_section = |order: [u64; 2]| {
        render_once(
            top_down_view_proj(),
            &[SectionData {
                pos: (0, 4, 0),
                opaque: vec![floor_cell(8, 8, MAT_RED)],
                water: order.to_vec(),
            }],
        )
    };
    let Some(near_first) = same_section([bright, dim]) else {
        return;
    };
    let far_first = same_section([dim, bright]).expect("首个场景已成功建过渲染器");
    assert_ne!(
        near_first, far_first,
        "区段内不得逐面排序：交换上传顺序必须改变画面"
    );

    // 拆进两个区段：(0,5,0) 的水面（世界 y=17）离相机更近，(0,4,0) 的更远。
    let near_section = SectionData {
        pos: (0, 5, 0),
        opaque: vec![],
        water: vec![water_cell(8, 0, 8, 0xFF)],
    };
    let far_section = SectionData {
        pos: (0, 4, 0),
        opaque: vec![floor_cell(8, 8, MAT_RED)],
        water: vec![water_cell(8, 2, 8, 0x88)],
    };
    let listed_near_first = render_once(
        top_down_view_proj(),
        &[
            SectionData {
                pos: near_section.pos,
                opaque: near_section.opaque.clone(),
                water: near_section.water.clone(),
            },
            SectionData {
                pos: far_section.pos,
                opaque: far_section.opaque.clone(),
                water: far_section.water.clone(),
            },
        ],
    )
    .expect("首个场景已成功建过渲染器");
    let listed_far_first =
        render_once(top_down_view_proj(), &[far_section, near_section]).expect("同上");
    assert_eq!(
        listed_near_first, listed_far_first,
        "跨区段必须按距离由远及近绘制，与 visible 列表的次序无关"
    );
}

/// water pass 是系统里唯一新增的半透明阶段。
///
/// 用源码清单钉住：`render_frame` 里出现的 render pass 标签必须与这份名单
/// 逐条相等。任何人再加一个 pass 都要来改这份名单，从而被迫回答
/// 「`voxel-visual-presentation` 只放宽了**恰好一个**额外半透明阶段」这件事。
#[test]
fn water_is_the_only_added_render_pass() {
    let source = include_str!("mod.rs");
    let labels: Vec<&str> = source
        .match_indices("begin_render_pass(&wgpu::RenderPassDescriptor {")
        .filter_map(|(at, _)| {
            let tail = &source[at..];
            let start = tail.find("label: Some(\"")? + "label: Some(\"".len();
            let end = start + tail[start..].find('"')?;
            Some(&tail[start..end])
        })
        .collect();
    assert_eq!(
        labels,
        vec!["terrain pass", "water pass", "damage overlay pass"],
        "render_frame 的 render pass 名单变了：新增额外的半透明阶段需要先修订 \
         voxel-visual-presentation 的边界"
    );
}
