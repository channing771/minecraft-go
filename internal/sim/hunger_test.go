package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestApplyExhaustionExhaustive 是疲劳结算的穷举表：三层状态的每种起始组合
// 乘上四种累加量，逐条断言**精确**的三个字段末值。
//
// 为什么必须逐条写死期望值而不是用一个「期望函数」算出来：期望函数只会是被测
// 实现的第二份拷贝，两份一起错时测试照样全绿。
//
// 四种累加量刻意跨越阈值的三个区间：0 与 3999 不跨（读数必须原样累加）、
// 4000 恰好跨一次、8000 跨两次。**只测不跨阈值的量是本变更最大的假绿点**：
// 疲劳不跨阈值时，「扣饱和」与「不扣饱和」两种实现的读数完全相同，差值恒等。
//
// 饥饿 0 配饱和 1000 是刻意的越界探针：它本身违反「饱和度 ≤ 饥饿值×1000」这条
// 不变量，放进表里是为了钉住 applyExhaustion 的分支次序——先看饱和度、饱和度
// 归零后才动饥饿值——与饥饿值当前取值无关。
func TestApplyExhaustionExhaustive(t *testing.T) {
	const threshold = defaultExhaustionThresholdMilli
	for _, tc := range []struct {
		name                           string
		hunger                         uint8
		saturation, exhaustion         uint16
		add                            uint16
		wantHunger                     uint8
		wantSaturation, wantExhaustion uint16
	}{
		// 饥饿 0 / 饱和 0：没有任何可消耗的资源，跨阈值只把疲劳清零。
		{"饥饿0饱和0加0", 0, 0, 0, 0, 0, 0, 0},
		{"饥饿0饱和0加3999", 0, 0, 0, 3999, 0, 0, 3999},
		{"饥饿0饱和0加4000", 0, 0, 0, 4000, 0, 0, 0},
		{"饥饿0饱和0加8000", 0, 0, 0, 8000, 0, 0, 0},

		// 饥饿 0 / 饱和 1000（越界探针）：饱和度仍然先被消耗。
		{"饥饿0饱和1000加0", 0, 1000, 0, 0, 0, 1000, 0},
		{"饥饿0饱和1000加3999", 0, 1000, 0, 3999, 0, 1000, 3999},
		{"饥饿0饱和1000加4000", 0, 1000, 0, 4000, 0, 0, 0},
		{"饥饿0饱和1000加8000", 0, 1000, 0, 8000, 0, 0, 0},

		// 饥饿 1 / 饱和 0：第一次跨阈值把饥饿值扣到 0，之后不再下降。
		{"饥饿1饱和0加0", 1, 0, 0, 0, 1, 0, 0},
		{"饥饿1饱和0加3999", 1, 0, 0, 3999, 1, 0, 3999},
		{"饥饿1饱和0加4000", 1, 0, 0, 4000, 0, 0, 0},
		{"饥饿1饱和0加8000", 1, 0, 0, 8000, 0, 0, 0},

		// 饥饿 1 / 饱和 1000：一次跨阈值只烧饱和度，两次才动饥饿值。
		{"饥饿1饱和1000加0", 1, 1000, 0, 0, 1, 1000, 0},
		{"饥饿1饱和1000加3999", 1, 1000, 0, 3999, 1, 1000, 3999},
		{"饥饿1饱和1000加4000", 1, 1000, 0, 4000, 1, 0, 0},
		{"饥饿1饱和1000加8000", 1, 1000, 0, 8000, 0, 0, 0},

		// 饥饿满 / 饱和 0：每次跨阈值恰好扣 1 点饥饿。
		{"饥饿20饱和0加0", 20, 0, 0, 0, 20, 0, 0},
		{"饥饿20饱和0加3999", 20, 0, 0, 3999, 20, 0, 3999},
		{"饥饿20饱和0加4000", 20, 0, 0, 4000, 19, 0, 0},
		{"饥饿20饱和0加8000", 20, 0, 0, 8000, 18, 0, 0},

		// 饥饿满 / 饱和 1 点：spec Scenario「疲劳先消耗饱和度」的直接编码。
		{"饥饿20饱和1000加0", 20, 1000, 0, 0, 20, 1000, 0},
		{"饥饿20饱和1000加3999", 20, 1000, 0, 3999, 20, 1000, 3999},
		{"饥饿20饱和1000加4000", 20, 1000, 0, 4000, 20, 0, 0},
		{"饥饿20饱和1000加8000", 20, 1000, 0, 8000, 19, 0, 0},

		// 饥饿满 / 饱和满：只烧饱和度，饥饿值纹丝不动。
		{"饥饿20饱和满加0", 20, 20000, 0, 0, 20, 20000, 0},
		{"饥饿20饱和满加3999", 20, 20000, 0, 3999, 20, 20000, 3999},
		{"饥饿20饱和满加4000", 20, 20000, 0, 4000, 20, 19000, 0},
		{"饥饿20饱和满加8000", 20, 20000, 0, 8000, 20, 18000, 0},

		// 不足一点的残余饱和度：整点扣减把它清零，且**不**顺手扣饥饿值
		// （一次跨阈值最多消耗一种资源）。第二次跨阈值才轮到饥饿值。
		{"残余饱和500加4000", 20, 500, 0, 4000, 20, 0, 0},
		{"残余饱和500加8000", 20, 500, 0, 8000, 19, 0, 0},

		// 起始疲劳非零：累加必须叠在既有读数之上，不是覆盖。
		{"起始3999加1恰好跨", 20, 1000, 3999, 1, 20, 0, 0},
		{"起始3999加0不跨", 20, 1000, 3999, 0, 20, 1000, 3999},
		{"起始2000加2001跨一次", 20, 1000, 2000, 2001, 20, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			player := &playerState{
				hunger:          tc.hunger,
				saturationMilli: tc.saturation,
				exhaustionMilli: tc.exhaustion,
			}
			player.applyExhaustion(tc.add, threshold)
			if player.hunger != tc.wantHunger ||
				player.saturationMilli != tc.wantSaturation ||
				player.exhaustionMilli != tc.wantExhaustion {
				t.Fatalf("applyExhaustion(%d) 后 (饥饿,饱和,疲劳)=(%d,%d,%d)，想要 (%d,%d,%d)",
					tc.add, player.hunger, player.saturationMilli, player.exhaustionMilli,
					tc.wantHunger, tc.wantSaturation, tc.wantExhaustion)
			}
		})
	}
}

// TestApplyExhaustionReadsThresholdFromParameter 钉住「阈值来自本 tick 的
// tunable 快照，不是写死的编译期常量」：同一个累加量在两个不同阈值下必须给出
// 不同的结果。若实现把 4000 写死，把阈值调到 8000 的那一半会当场变红。
func TestApplyExhaustionReadsThresholdFromParameter(t *testing.T) {
	small := &playerState{hunger: 20, saturationMilli: 5000}
	small.applyExhaustion(4000, 4000)
	if small.saturationMilli != 4000 || small.exhaustionMilli != 0 {
		t.Fatalf("阈值 4000 下 (饱和,疲劳)=(%d,%d)，想要 (4000,0)",
			small.saturationMilli, small.exhaustionMilli)
	}
	large := &playerState{hunger: 20, saturationMilli: 5000}
	large.applyExhaustion(4000, 8000)
	if large.saturationMilli != 5000 || large.exhaustionMilli != 4000 {
		t.Fatalf("阈值 8000 下 (饱和,疲劳)=(%d,%d)，想要 (5000,4000)：4000 不应跨过 8000 的阈值",
			large.saturationMilli, large.exhaustionMilli)
	}
}

// TestApplyExhaustionZeroThresholdDoesNotHang 是权威 tick 内的死循环兜底：
// 阈值 0 会让「while 疲劳 >= 阈值」永不退出。配置层已把下限钳到 1000，但 sim
// 按架构约束不得导入 config，那道钳制隔着一个包，本包必须自己兜底。
//
// 与 advanceOxygen 的 max(…, 1) 同形。
func TestApplyExhaustionZeroThresholdDoesNotHang(t *testing.T) {
	player := &playerState{hunger: 20, saturationMilli: 20000}
	player.applyExhaustion(3, 0)
	if player.exhaustionMilli != 0 {
		t.Fatalf("阈值 0 时疲劳=%d，想要被兜底阈值消化为 0", player.exhaustionMilli)
	}
	if player.saturationMilli != 17000 {
		t.Fatalf("阈值 0 兜底为 1 时饱和=%d，想要 17000（3 次跨阈值）", player.saturationMilli)
	}
}

// TestNewPlayerStartsFedAndUnexhausted 覆盖新玩家/重生的固定初值。
func TestNewPlayerStartsFedAndUnexhausted(t *testing.T) {
	const id = SessionID(41)
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	player := engine.sessions[id].player
	if player.hunger != core.MaxHunger {
		t.Fatalf("新玩家饥饿=%d，想要 %d", player.hunger, core.MaxHunger)
	}
	if player.saturationMilli != initialSaturationMilli {
		t.Fatalf("新玩家饱和=%d，想要 %d", player.saturationMilli, initialSaturationMilli)
	}
	if player.exhaustionMilli != 0 || player.starvationTicks != 0 {
		t.Fatalf("新玩家 (疲劳,饥饿计时)=(%d,%d)，想要 (0,0)",
			player.exhaustionMilli, player.starvationTicks)
	}
}
