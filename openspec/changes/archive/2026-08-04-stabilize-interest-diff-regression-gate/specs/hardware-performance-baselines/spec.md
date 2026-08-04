## ADDED Requirements

### Requirement: 同硬件回归只比较统计稳定指标
对于 scenario v6 及后续版本的同硬件、同 transport 报告，性能比较器 MUST 只对具有稳定语义和可重复证据的字段执行相对回归比较。历史 `interest_diff` 字段 MUST 继续接受报告完整性校验，但其 p50、p95、p99 和 max 不得作为相对回归失败依据；该字段表示单会话完整发布时间，不得解释为纯兴趣差分耗时。

#### Scenario: 单会话发布时间尾部波动不阻断回归
- **GIVEN** 两份同硬件、同 scenario、同 transport 的完整报告仅有 `interest_diff` 分位数相对变化超过配置阈值
- **WHEN** 性能比较器执行同场景回归比较
- **THEN** 比较 MUST 不因 `interest_diff` 的 p50、p95、p99 或 max 失败

#### Scenario: 单会话发布时间仍须完整有效
- **GIVEN** current 报告的 `interest_diff` 样本不足、数值非正或分位数不单调
- **WHEN** 性能比较器校验该报告
- **THEN** 比较 MUST 拒绝该报告并说明完整性错误

#### Scenario: 其他稳定服务端指标仍然判退化
- **GIVEN** 两份同硬件、同 scenario、同 transport 的完整报告中，server tick 的适用分位数相对退化严格超过配置阈值
- **WHEN** 性能比较器执行同场景回归比较
- **THEN** 比较 MUST 失败且指出对应 server tick 指标

### Requirement: 比较契约修正不得通过重新采样获利
当一份不可变正式报告只因已证实不稳定的相对指标失败，而 benchmark producer、工作负载、报告 schema 和绝对门禁均未变化时，项目 SHALL 在修正比较契约后重新判定该报告的原始字节，不得重新运行同一步采样、改写报告或覆盖基线。后续尚未执行的正式步骤 MAY 在原始精确生产提交上按既有一次性规则继续。

#### Scenario: 重新判定既有 Memory 报告
- **GIVEN** M4D scenario v8 Memory 报告的哈希和原始字节保持不变，且旧比较只因 `interest_diff` 相对波动失败
- **WHEN** 修正后的比较器重新判定该报告
- **THEN** 系统 MUST 复用原始报告并执行全部仍适用的完整性、绝对和相对门禁，不得启动新的 Memory benchmark

#### Scenario: 继续唯一一次 TCP 步骤
- **GIVEN** 重新判定的 Memory 报告通过，原计划中的 TCP 报告尚未生成
- **WHEN** 正式链继续
- **THEN** 项目 MAY 从与 Memory 相同的原始精确生产提交生成一次无窗口 TCP 报告；任一步失败 MUST 立即停止且不得重跑或覆盖基线
