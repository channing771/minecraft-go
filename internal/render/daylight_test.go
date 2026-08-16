package render

import (
	_ "embed"
	"math"
	"testing"
)

func closeEnough(got, want float32) bool {
	return math.Abs(float64(got-want)) <= 1e-5
}

func closeDirection(got, want [3]float32) bool {
	return closeEnough(got[0], want[0]) && closeEnough(got[1], want[1]) && closeEnough(got[2], want[2])
}

// Mutation killed: 先将绝对时间压成 float32 会在大时间丢失每 80 tick 的连续性。
func TestCloudOffsetAdvancesOneBlockAtLargeTimes(t *testing.T) {
	for _, worldTime := range []uint64{1 << 28, 1 << 31, math.MaxUint64 - 159} {
		before := CloudOffsetAt(worldTime)
		want := before
		want.Local++
		if want.Local == cloudBlocksPerMacro {
			want.Local = 0
			want.MacroX++
		}
		if got := CloudOffsetAt(worldTime + cloudTicksPerBlock); got != want {
			t.Fatalf("worldTime=%d 的 80 tick 云偏移=%v，想要 %v", worldTime, got, want)
		}
	}
}

func TestCloudOffsetUsesAuthoritativeWorldTime(t *testing.T) {
	for _, test := range []struct {
		ticks uint64
		want  CloudOffset
	}{
		{0, CloudOffset{}},
		{1, CloudOffset{Local: 1.0 / 80.0}},
		{40, CloudOffset{Local: 0.5}},
		{79, CloudOffset{Local: 79.0 / 80.0}},
		{80, CloudOffset{Local: 1}},
		{160, CloudOffset{Local: 2}},
	} {
		if got := CloudOffsetAt(test.ticks); got != test.want {
			t.Fatalf("CloudOffsetAt(%d)=%v，想要 %v", test.ticks, got, test.want)
		}
	}
}

func TestCloudOffsetRollsMacroEverySixtyFourBlocks(t *testing.T) {
	if got, want := CloudOffsetAt((cloudBlocksPerMacro-1)*cloudTicksPerBlock), (CloudOffset{Local: cloudBlocksPerMacro - 1}); got != want {
		t.Fatalf("rollover 前偏移=%v，想要 %v", got, want)
	}
	if got, want := CloudOffsetAt(cloudBlocksPerMacro*cloudTicksPerBlock), (CloudOffset{MacroX: 1}); got != want {
		t.Fatalf("rollover 后偏移=%v，想要 %v", got, want)
	}
}

func TestDayNightPhaseFormula(t *testing.T) {
	tests := []struct {
		name         string
		worldTime    uint64
		wantSun      float32
		wantDaylight float32
	}{
		{"黎明", 0, 0, 0.15},
		{"正午", 6000, 1, 1},
		{"黄昏", 12000, 0, 0.15},
		{"午夜", 18000, 0, 0.15},
		{"跨周期正午", DayLengthTicks + 6000, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DayNightAt(tc.worldTime)
			if !closeEnough(got.Sun, tc.wantSun) {
				t.Fatalf("sun = %v，想要 %v", got.Sun, tc.wantSun)
			}
			if !closeEnough(got.Daylight, tc.wantDaylight) {
				t.Fatalf("daylight = %v，想要 %v", got.Daylight, tc.wantDaylight)
			}
		})
	}
}

func TestDayNightSunIsClampedAtNight(t *testing.T) {
	// 相位 12000..24000 太阳位于地平线以下，sun 必须被夹到 0。
	for phase := uint64(12001); phase < DayLengthTicks; phase += 137 {
		got := DayNightAt(phase)
		if got.Sun != 0 {
			t.Fatalf("相位 %d 的 sun = %v，想要 0", phase, got.Sun)
		}
		if !closeEnough(got.Daylight, 0.15) {
			t.Fatalf("相位 %d 的 daylight = %v，想要 0.15", phase, got.Daylight)
		}
	}
}

func TestDayNightIsPeriodicAndFinite(t *testing.T) {
	for phase := uint64(0); phase < DayLengthTicks; phase += 251 {
		base := DayNightAt(phase)
		next := DayNightAt(phase + DayLengthTicks)
		far := DayNightAt(phase + 1000*DayLengthTicks)
		if base != next || base != far {
			t.Fatalf("相位 %d 不是周期性的：%+v / %+v / %+v", phase, base, next, far)
		}
		values := []float32{base.Sun, base.Daylight}
		values = append(values, base.ClearColor[:]...)
		for _, value := range values {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				t.Fatalf("相位 %d 产生非有限值：%+v", phase, base)
			}
			if value < 0 || value > 1 {
				t.Fatalf("相位 %d 的值 %v 超出 [0,1]", phase, value)
			}
		}
	}
}

func TestDayNightClearColorInterpolatesBetweenNightAndDay(t *testing.T) {
	noon := DayNightAt(6000).ClearColor
	wantDay := [4]float32{0.42, 0.68, 0.92, 1}
	for index := range noon {
		if !closeEnough(noon[index], wantDay[index]) {
			t.Fatalf("正午 clear color = %v，想要 %v", noon, wantDay)
		}
	}
	midnight := DayNightAt(18000).ClearColor
	wantNight := [4]float32{0.02, 0.03, 0.08, 1}
	for index := range midnight {
		if !closeEnough(midnight[index], wantNight[index]) {
			t.Fatalf("午夜 clear color = %v，想要 %v", midnight, wantNight)
		}
	}
}

func TestCelestialDirectionsFollowAuthoritativePhase(t *testing.T) {
	tests := []struct {
		name           string
		worldTime      uint64
		wantSun        [3]float32
		wantMoon       [3]float32
		wantSunLevel   float32
		wantDaylight   float32
		wantClearColor [4]float32
	}{
		{"日出", 0, [3]float32{1, 0, 0}, [3]float32{-1, 0, 0}, 0, 0.15, [4]float32{0.02, 0.03, 0.08, 1}},
		{"正午", 6000, [3]float32{0, 1, 0}, [3]float32{0, -1, 0}, 1, 1, [4]float32{0.42, 0.68, 0.92, 1}},
		{"日落", 12000, [3]float32{-1, 0, 0}, [3]float32{1, 0, 0}, 0, 0.15, [4]float32{0.02, 0.03, 0.08, 1}},
		{"午夜", 18000, [3]float32{0, -1, 0}, [3]float32{0, 1, 0}, 0, 0.15, [4]float32{0.02, 0.03, 0.08, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DayNightAt(tc.worldTime)
			if !closeDirection(got.SunDirection, tc.wantSun) || !closeDirection(got.MoonDirection, tc.wantMoon) {
				t.Fatalf("天体方向 = sun %v moon %v，想要 sun %v moon %v", got.SunDirection, got.MoonDirection, tc.wantSun, tc.wantMoon)
			}
			if !closeEnough(got.Sun, tc.wantSunLevel) || !closeEnough(got.Daylight, tc.wantDaylight) || got.ClearColor != tc.wantClearColor {
				t.Fatalf("既有昼夜值 = %+v，想要 sun=%v daylight=%v clear=%v", got, tc.wantSunLevel, tc.wantDaylight, tc.wantClearColor)
			}
		})
	}
}

func TestCelestialStarVisibilitySmoothlyAppearsNearHorizonAndAtNight(t *testing.T) {
	midnight := DayNightAt(18000).StarVisibility
	nearSunrise := DayNightAt(500).StarVisibility
	nearSunset := DayNightAt(11500).StarVisibility
	day := DayNightAt(1000).StarVisibility
	if midnight != 1 {
		t.Fatalf("午夜星空可见度 = %v，想要 1", midnight)
	}
	if nearSunrise <= 0 || nearSunrise >= 1 || !closeEnough(nearSunrise, nearSunset) {
		t.Fatalf("近地平线星空可见度 = %v/%v，想要相等且严格介于 0 与 1", nearSunrise, nearSunset)
	}
	if day != 0 {
		t.Fatalf("日间星空可见度 = %v，想要 0", day)
	}
}

func TestTerrainBrightnessMatchesSpecification(t *testing.T) {
	tests := []struct {
		name     string
		phase    uint64
		sky      uint8
		wantBase float32
	}{
		{"正午露天全亮", 6000, 15, 1},
		{"正午遮蔽保留室内亮度", 6000, 0, 0.08},
		{"午夜露天最低可见度", 18000, 15, 0.15},
		{"午夜遮蔽室内亮度", 18000, 0, 0.08},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			daylight := DayNightAt(tc.phase).Daylight
			if got := TerrainBrightness(daylight, tc.sky); !closeEnough(got, tc.wantBase) {
				t.Fatalf("地形基础亮度 = %v，想要 %v", got, tc.wantBase)
			}
		})
	}

	// 中间天空光按线性插值落在室内与露天之间。
	daylight := DayNightAt(6000).Daylight
	mid := TerrainBrightness(daylight, 8)
	if mid <= TerrainBrightness(daylight, 0) || mid >= TerrainBrightness(daylight, 15) {
		t.Fatalf("中间天空光亮度 = %v，想要严格落在 0.08 与 1 之间", mid)
	}
}
