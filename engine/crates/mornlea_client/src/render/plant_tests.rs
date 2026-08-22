//! 植物交叉斜面的离屏对照测试（无窗口，走既有 `OffscreenRenderer` + `readback`）。
//!
//! 结构与 `water_tests.rs` 同源，但**不复用**它的私有夹具：那里的相机是正交俯视、
//! atlas 只有三层，而植物的可辨识性恰恰要靠水平视角与两种可区分的材质。位打包
//! 同样手写一份——位布局是 engine 与 client 两个 crate 之间的契约，任一侧改位而
//! 不改另一侧，这些用例会以图像不符的形式报出来。
//!
//! 四层纯色 atlas：0 = 不透明红（轴向对照面）、1 = 半透明蓝（占位，保持与
//! water_tests 同序）、2 = 不透明绿（对角线 A）、3 = 不透明黄（对角线 B）。
//!
//! **为什么给两条对角线不同材质**：从任一水平轴向观察，一株植物的两片斜面与
//! 一整块轴向面的**轮廓完全相同**，靠"画了多少像素"分辨不出来。而真正要防的
//! 回归是「某个方向少了一片」——那必须能把两片分开看。材质只决定采样哪一层
//! atlas，着色器与 cull 对植物的处理都只看 `face`，因此这样上色不改变被测路径。

use super::*;

/// 对角线 A / B 的 face 编号，与 engine `quad.rs` 的 `Face::PlantDiagA/B` 一致。
const FACE_PLANT_A: u32 = 6;
const FACE_PLANT_B: u32 = 7;
/// PosY（朝上的顶面）的 face 编号，轴向对照面用。
const FACE_POS_Y: u32 = 3;

const MAT_RED: u16 = 0;
const MAT_GREEN: u16 = 2;
const MAT_YELLOW: u16 = 3;

/// 一个 atlas 层（含全部 mip）的字节数：16²+8²+4²+2²+1² 个 RGBA 像素。
const ATLAS_LAYER_BYTES: usize = (256 + 64 + 16 + 4 + 1) * 4;
/// 离屏画面边长。
const VIEW: u32 = 64;
/// 被观察的区段。**`pos.1` 必须是 4**：区段原点是 `pos * 16 + WORLD_MIN_Y`，
/// 而 `WORLD_MIN_Y = -64`，取 4 才让原点落在世界 `y = 0`、局部坐标等于世界坐标。
/// 写成 `(0,0,0)` 会把整格挪到世界 `y = -56`，凡是让 `clip.x`/`clip.y` 依赖
/// `world.y` 的投影都会把它甩出画面，症状是"什么都没画"。
const SECTION: (i32, i32, i32) = (0, 4, 0);
/// 被观察格在区段里的局部坐标；区段原点在世界原点，所以它也是世界坐标。
const CELL: (u8, u8, u8) = (8, 8, 8);
/// 被观察格的中心世界坐标。
const CELL_CENTER: [f32; 3] = [8.5, 8.5, 8.5];

/// pack_quad 复刻 `mornlea_engine::quad::Quad::pack` 的**植物与普通两条**位布局。
///
/// | 位 | 普通 quad | 植物 quad |
/// |---|---|---|
/// | 0..11 | x / y / z | 同左 |
/// | 12 | w-1 的最低位 | 正/背标志（0 = 正、1 = 背） |
/// | 13..19 | w-1 高位与 h-1 | 保留，必须为 0 |
/// | 20..22 | face 0..5 | face 6 / 7 |
/// | 23..38 | material | 同左 |
/// | 39..46 | ao | 同左 |
/// | 47..54 | light | 同左 |
/// | 63 | 永久留空 | 永久留空 |
#[allow(clippy::too_many_arguments)]
fn pack_quad(x: u8, y: u8, z: u8, w: u8, h: u8, face: u32, material: u16, back: bool) -> u64 {
    let low = if face >= 6 {
        assert!(w == 1 && h == 1, "植物 quad 必须是 1×1");
        u64::from(back) << 12
    } else {
        assert!(!back, "非植物 quad 不得设置正背标志");
        u64::from(w - 1) << 12 | u64::from(h - 1) << 16
    };
    let packed = u64::from(x)
        | u64::from(y) << 4
        | u64::from(z) << 8
        | low
        | u64::from(face) << 20
        | u64::from(material) << 23
        | 0xFFu64 << 39
        | 0xFFu64 << 47;
    assert_eq!(packed >> 63, 0, "quad 占用了必须留空的 bit 63");
    packed
}

/// plant_cell 返回一株植物的四条 quad：两条对角线 × 正反两面。
///
/// 次序与 engine `greedy.rs` 的 `PLANT_QUADS` 一致。
fn plant_cell() -> Vec<u64> {
    let (x, y, z) = CELL;
    vec![
        pack_quad(x, y, z, 1, 1, FACE_PLANT_A, MAT_GREEN, false),
        pack_quad(x, y, z, 1, 1, FACE_PLANT_A, MAT_GREEN, true),
        pack_quad(x, y, z, 1, 1, FACE_PLANT_B, MAT_YELLOW, false),
        pack_quad(x, y, z, 1, 1, FACE_PLANT_B, MAT_YELLOW, true),
    ]
}

/// axial_top_cell 返回同一格的一条轴向顶面，作为"植物退回轴向满面"的对照。
fn axial_top_cell() -> Vec<u64> {
    let (x, y, z) = CELL;
    vec![pack_quad(x, y, z, 1, 1, FACE_POS_Y, MAT_RED, false)]
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

/// 建渲染器、上传 atlas 与一个区段、渲染**一帧**并回读 BGRA 图像。
///
/// 每次都新建渲染器：首帧 `have_last_camera` 为假，HiZ 必然停用，图像因此只取决于
/// 本用例的输入。无 GPU 适配器时返回 None（调用方跳过，与既有约定一致）。
fn render(camera_pos: [f32; 3], view_proj: [f32; 16], quads: &[u64]) -> Option<Vec<u8>> {
    let mut renderer = OffscreenRenderer::new(VIEW, VIEW).ok()?;
    let colors = [
        [200u8, 60, 60, 255],  // 层 0：不透明红
        [40u8, 90, 200, 160],  // 层 1：半透明蓝
        [60u8, 200, 60, 255],  // 层 2：不透明绿
        [220u8, 200, 60, 255], // 层 3：不透明黄
    ];
    assert!(renderer.upload_atlas(colors.len() as u32, &atlas_bytes(&colors)));
    assert!(renderer.upload_section(SECTION, &quad_bytes(quads), &[]));
    let mut identity = [0.0f32; 16];
    for i in 0..4 {
        identity[i * 4 + i] = 1.0;
    }
    let frame = FrameInput {
        view_proj,
        view_proj_inv: identity,
        pos: camera_pos,
        daylight: 1.0,
        sun_direction: [0.0, 1.0, 0.0],
        star_visibility: 0.0,
        sky_color: [0.25, 0.5, 1.0, 1.0],
        cloud_macro_x: 0,
        cloud_local: 0.0,
        visible: vec![SECTION],
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

/// 数一幅图里"以某个通道为主导"的像素数。
///
/// 判据取通道间的相对关系而不是精确色值：地形着色会乘上 shade 与光照，精确匹配
/// 会因任何一处系数微调而误报。红 / 绿 / 黄三层的主导通道两两可分。
fn count_dominant(image: &[u8], want: Dominant) -> usize {
    let mut count = 0;
    for row in 0..VIEW {
        for x in 0..VIEW {
            let [r, g, b] = pixel(image, x, row).map(i32::from);
            let matched = match want {
                Dominant::Red => r > 100 && r > g + 40 && r > b + 40,
                Dominant::Green => g > 100 && g > r + 40 && g > b + 40,
                Dominant::Yellow => r > 100 && g > 100 && r > b + 40 && g > b + 40,
            };
            if matched {
                count += 1;
            }
        }
    }
    count
}

#[derive(Clone, Copy)]
enum Dominant {
    Red,
    Green,
    Yellow,
}

/// side_view_proj 造一台**水平**正交相机，视线方向为 `(dx, 0, dz)`。
///
/// ```text
/// clip.x = (右向量 · (world - 格心)) * 0.5      右向量 = up × forward = (dz, 0, -dx)
/// clip.y = (world.y - 格心.y) * 0.5
/// clip.z = 0.5 + (forward · (world - 格心)) / 200   （越远越大，全程落在 (0,1)）
/// ```
///
/// 于是被观察的那一格恰好落在画面正中的 16×16 像素方格里，四个方向的构图完全
/// 对称——这正是"从四个方向读数可比"的前提。矩阵是列主序，`m[列*4 + 行]`。
fn side_view_proj(dx: f32, dz: f32) -> [f32; 16] {
    let (rx, rz) = (dz, -dx);
    let c = CELL_CENTER;
    let mut m = [0.0f32; 16];
    m[0] = rx * 0.5;
    m[8] = rz * 0.5;
    m[12] = -(rx * c[0] + rz * c[2]) * 0.5;
    m[5] = 0.5;
    m[13] = -c[1] * 0.5;
    m[2] = dx / 200.0;
    m[10] = dz / 200.0;
    m[14] = 0.5 - (dx * c[0] + dz * c[2]) / 200.0;
    m[15] = 1.0;
    m
}

/// sheared_top_view_proj 造一台**近乎俯视、带一点水平错切**的正交相机。
///
/// ```text
/// clip.x = (world.x + 0.15 * (world.y - 8)) * 0.5 - 4
/// clip.y = world.z * 0.5 - 4
/// clip.z = 0.5 - world.y / 200
/// ```
///
/// 纯俯视下两片斜面正好侧对相机、投影退化成零面积，光栅化可能一个像素都不产生，
/// 那样"植物比轴向面少画很多"就成了"植物什么都没画"的同义反复。掺进
/// `0.15 * world.y` 的错切之后，每片斜面变成宽约 `0.15 * 0.5 * 32 = 2.4` 像素的
/// 斜带、两片交叉成一个 X，而同一格的轴向顶面仍然是实心的 16×16 —— 两者的
/// 着色像素数因此相差数倍。
fn sheared_top_view_proj() -> [f32; 16] {
    let mut m = [0.0f32; 16];
    m[0] = 0.5; // clip.x 受 world.x 影响
    m[4] = 0.15 * 0.5; // clip.x 受 world.y 错切
    m[12] = -4.0 - 0.15 * 0.5 * 8.0;
    m[9] = 0.5; // clip.y 受 world.z 影响
    m[13] = -4.0;
    m[6] = -0.005; // clip.z 受 world.y 影响
    m[14] = 0.5;
    m[15] = 1.0;
    m
}

/// Scenario「从四个水平方向都能看到植物」。
///
/// 判据是**两片斜面都在**，而不是"画面上有东西"：从任一水平轴向看过去，一株植物
/// 的两片斜面与一整块轴向面轮廓完全相同，只数像素分辨不出少了哪一片。而真正要防
/// 的回归恰恰是"某个方向少一片"——`cull.wgsl` 若沿用轴向法线处理 face 6/7，
/// `face >> 1` 会得到与几何无关的 3，两条对角线里必有一条被判成背面剔掉，
/// 而画面上仍然"看得见植物"。只有分色计数才会红。
#[test]
fn plant_shows_both_diagonals_from_all_four_horizontal_directions() {
    let quads = plant_cell();
    let mut readings = Vec::new();
    for (name, dx, dz) in [
        ("+X", 1.0, 0.0),
        ("-X", -1.0, 0.0),
        ("+Z", 0.0, 1.0),
        ("-Z", 0.0, -1.0),
    ] {
        let c = CELL_CENTER;
        let camera = [c[0] - dx * 100.0, c[1], c[2] - dz * 100.0];
        let Some(image) = render(camera, side_view_proj(dx, dz), &quads) else {
            return;
        };
        let green = count_dominant(&image, Dominant::Green);
        let yellow = count_dominant(&image, Dominant::Yellow);
        assert!(
            green > 0,
            "{name} 方向看不到对角线 A（绿）：green={green} yellow={yellow}"
        );
        assert!(
            yellow > 0,
            "{name} 方向看不到对角线 B（黄）：green={green} yellow={yellow}"
        );
        readings.push((name, green, yellow));
    }

    // 夹具承重守卫排在真实断言之后：同一台相机下把植物撤掉必须一个植物色像素
    // 都数不出来，否则上面四条只是在数背景。
    let empty = render(
        [CELL_CENTER[0] - 100.0, CELL_CENTER[1], CELL_CENTER[2]],
        side_view_proj(1.0, 0.0),
        &[],
    )
    .expect("首个场景已成功建过渲染器");
    assert_eq!(
        count_dominant(&empty, Dominant::Green) + count_dominant(&empty, Dominant::Yellow),
        0,
        "空场景也数出了植物色像素，计数口径无效"
    );

    // 四个方向构图完全对称，两片的可见面积应当彼此接近；某一方向大幅塌陷同样是
    // "少了一片"的信号，即便没有归零。
    let totals: Vec<usize> = readings
        .iter()
        .map(|(_, green, yellow)| green + yellow)
        .collect();
    let (minimum, maximum) = (*totals.iter().min().unwrap(), *totals.iter().max().unwrap());
    assert!(
        minimum * 2 >= maximum,
        "四个方向的可见面积相差过大：{readings:?}"
    );
}

/// Scenario「植物以交叉斜面呈现而非轴向满面」在像素上的落点。
///
/// 同一格、同一台相机，两次渲染只换几何：四条交叉斜面 vs 一条轴向顶面。近乎俯视
/// 时前者是两条细斜带交成的 X（实测 92 像素）、后者是实心的 16×16（256 像素）。
///
/// 这条正是「去掉按 material 判别、让植物退回轴向满面」那次变异的落点：变异之后
/// 植物的着色像素数会追平轴向对照，比值断言当场红。
#[test]
fn plant_covers_far_less_than_an_axial_face_from_above() {
    let camera = [2.0, 100.0, 8.0];
    let Some(plant) = render(camera, sheared_top_view_proj(), &plant_cell()) else {
        return;
    };
    let axial = render(camera, sheared_top_view_proj(), &axial_top_cell())
        .expect("首个场景已成功建过渲染器");

    let plant_painted =
        count_dominant(&plant, Dominant::Green) + count_dominant(&plant, Dominant::Yellow);
    let axial_painted = count_dominant(&axial, Dominant::Red);

    // 夹具承重守卫：轴向对照必须真的铺满那一格（16×16 = 256 像素），否则比值
    // 断言的分母是个偶然的小数字。
    assert!(
        axial_painted >= 200,
        "轴向对照面只着色了 {axial_painted} 个像素，夹具没有铺满整格"
    );
    assert!(
        plant_painted > 0,
        "植物一个像素都没画：交叉斜面必须有可见面积，否则下面的比值恒成立"
    );
    assert!(
        plant_painted * 2 < axial_painted,
        "植物着色 {plant_painted} 像素、轴向对照 {axial_painted} 像素：\
         植物没有被摆到对角面上，而是画成了一整块轴向面"
    );
}

/// 每条植物面实例仍是 8 字节，且 bit 63 仍然留空。
///
/// `pack_quad` 内部已就每条断言过 bit 63；这里把"实例宽度"这件事本身钉死，
/// 顺带覆盖正/背标志与保留位的编码：保留位一旦被写脏，engine 与 Go 侧的解包
/// 都会当场拒绝。
#[test]
fn plant_face_instances_stay_eight_bytes() {
    let quads = plant_cell();
    assert_eq!(quads.len(), 4, "每格恰好 4 条面实例");
    assert_eq!(
        quad_bytes(&quads).len(),
        quads.len() * std::mem::size_of::<u64>()
    );
    for quad in &quads {
        assert_eq!(quad >> 63, 0, "占用了必须留空的 bit 63");
        assert_eq!(
            (quad >> 13) & 0x7F,
            0,
            "植物 quad 的保留位 13..19 必须为 0：{quad:#018x}"
        );
        assert!((6..=7).contains(&((quad >> 20) & 7)), "face 必须是 6 或 7");
    }
    // 正/背两条必须真的落在 bit 12 上，各出现两次。
    let backs = quads.iter().filter(|quad| (*quad >> 12) & 1 == 1).count();
    assert_eq!(backs, 2, "四条里必须恰好两条是背面");
}
