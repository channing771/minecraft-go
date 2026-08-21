## MODIFIED Requirements

### Requirement: 缺失玩家材料包
系统 SHALL 只在玩家存档明确不存在时构造一次初始材料包：固定 27 格背包的前 14 格 MUST 依稳定材料清单顺序各包含 `64` 个对应物品，紧随其后的一格 MUST 包含可用于耕种的种子，九格快捷栏 MUST 保持为空。在草丛等自然种子来源存在之前，该格是玩家取得第一颗种子的唯一途径；自然来源上线后本条 MUST 重新评估。材料包 MUST 通过现有背包合法性规则，并仍由服务端权威确认与持久化。

#### Scenario: 缺失玩家获得固定材料包
- **GIVEN** `LoadPlayer` 返回 `ErrPlayerNotFound`
- **WHEN** 准备玩家快照
- **THEN** 快捷栏 MUST 为空且背包前 14 格 MUST 按固定顺序各含 64 个材料

#### Scenario: 已有玩家与未确认登录不被补发
- **GIVEN** 已有玩家或未确认登录
- **WHEN** 恢复、迁移或断开
- **THEN** 已有背包 MUST 不变且未确认材料包 MUST 不持久化或累加

#### Scenario: 确认后的新玩家不会重复获得材料
- **GIVEN** 新玩家已确认登录并保存包含初始材料包的快照
- **WHEN** 该玩家再次登录
- **THEN** 系统 MUST 恢复已保存背包，且 MUST NOT 再次填充或累加材料

#### Scenario: 材料包含有起步种子
- **GIVEN** `LoadPlayer` 返回 `ErrPlayerNotFound`
- **WHEN** 准备玩家快照
- **THEN** 背包 MUST 包含至少一格可用于耕种的种子，使该玩家无需任何自然来源即可开始耕种
