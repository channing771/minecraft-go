# Tasks: rust-engine-physics-step

- [ ] 1. OpenSpec 脚手架 → openspec validate --all --strict --no-interactive
- [ ] 2. Rust collision.rs 拆分 resolve_collision_parts → cargo test --workspace --locked
- [ ] 3. Rust step.rs 输入解析与校验（含单测）→ cargo test --workspace --locked
- [ ] 4. Rust step.rs 积分镜像（含锚点单测）→ cargo test --workspace --locked
- [ ] 5. Rust ffi 入口 + C header + ABI v2 → make rust && cargo test --workspace --locked
- [ ] 6. Go nativeabi.PhysicsStep 绑定（含原子失败测试）→ go test ./internal/nativeabi -race -count=1
- [ ] 7. Go 积分 oracle 提取 + 差分断言重构 → go test ./internal/physics -race -count=1
- [ ] 8. Go 生产 Step 切 native（布局测试 TDD）→ go test ./internal/physics ./internal/nativeabi -race -count=1
- [ ] 9. step 级差分语料扩展 → go test ./internal/physics -race -count=1
- [ ] 10. 收尾验证与基线文档 → make rust-check; go test ./... -race; go vet ./...; gofmt -l .; openspec validate --all --strict --no-interactive
