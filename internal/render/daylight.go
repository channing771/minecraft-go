package render

import "math"

// DayLengthTicks 是一个完整昼夜的权威 tick 数。
const DayLengthTicks = 24000

// indoorBrightness 是完全没有直射天空光时的地形基础亮度。
const indoorBrightness = 0.08

var (
	// nightSkyColor 是午夜天空背景色，dayCloudColor 是既有日间 clear color。
	nightSkyColor = [4]float32{0.02, 0.03, 0.08, 1}
	daySkyColor   = [4]float32{0.42, 0.68, 0.92, 1}
)

// DayNight 是某个显示相位下与世界空间明暗有关的全部固定值。
// 它完全由绝对世界时间决定，不含任何可变状态。
type DayNight struct {
	Sun            float32
	Daylight       float32
	ClearColor     [4]float32
	SunDirection   [3]float32
	MoonDirection  [3]float32
	StarVisibility float32
}

// DayNightAt 按固定曲线计算给定绝对世界时间的昼夜状态：
//
//	p        = worldTime mod 24000
//	sun      = max(0, sin(2πp/24000))
//	daylight = 0.15 + 0.85*sun
func DayNightAt(worldTime uint64) DayNight {
	phase := float64(worldTime%DayLengthTicks) / DayLengthTicks
	theta := 2 * math.Pi * phase
	sun := math.Sin(theta)
	if sun < 0 {
		sun = 0
	}
	starVisibility := 1 - (sun/0.25)*(sun/0.25)*(3-2*(sun/0.25))
	if sun >= 0.25 {
		starVisibility = 0
	}
	result := DayNight{
		Sun:            float32(sun),
		Daylight:       float32(0.15 + 0.85*sun),
		SunDirection:   [3]float32{float32(math.Cos(theta)), float32(math.Sin(theta)), 0},
		MoonDirection:  [3]float32{-float32(math.Cos(theta)), -float32(math.Sin(theta)), 0},
		StarVisibility: float32(starVisibility),
	}
	for index := range result.ClearColor {
		night, day := nightSkyColor[index], daySkyColor[index]
		result.ClearColor[index] = night + (day-night)*result.Sun
	}
	return result
}

// TerrainBrightness 返回天空光为 sky（0..15）的面在给定 daylight 下的基础亮度。
// 调用方仍需乘以既有的朝向系数与 AO。
func TerrainBrightness(daylight float32, sky uint8) float32 {
	return indoorBrightness + float32(sky)/15*(daylight-indoorBrightness)
}
