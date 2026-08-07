package server

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

// SetBlockForTest 直接写入权威世界的一个方块，仅供纵向测试构造固定场景。
func (server *Server) SetBlockForTest(position core.BlockPos, block core.BlockID) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	server.engine.SetBlockForTest(position, block)
}

// SetChunkFurnaceForTest 直接写入权威区块的熔炉槽，仅供纵向测试构造固定场景。
func (server *Server) SetChunkFurnaceForTest(
	key core.ChunkKey,
	slot int,
	value world.FurnaceSlot,
) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	server.engine.SetChunkFurnaceForTest(key, slot, value)
}

// SetChunkChestForTest 直接写入权威区块的箱子槽，仅供纵向测试构造固定场景。
func (server *Server) SetChunkChestForTest(
	key core.ChunkKey,
	slot int,
	value world.ChestSlot,
) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	server.engine.SetChunkChestForTest(key, slot, value)
}

// TouchChunkForTest 直接递增一个已加载区块的 revision 并标记为脏，仅供测试
// 把经由 SetChunkChestForTest/SetChunkFurnaceForTest 等原始状态覆写接入持久化的
// 保存/重试路径，而不必驱动一次真实的容器或方块命令。
func (server *Server) TouchChunkForTest(key core.ChunkKey) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	server.engine.TouchChunkForTest(key)
}

// SetPlayerInventoryForTest 用给定函数改写某个会话的权威物品状态，仅供纵向测试使用。
func (server *Server) SetPlayerInventoryForTest(
	session sim.SessionID,
	mutate func(core.Inventory) core.Inventory,
) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	server.engine.SetPlayerInventoryForTest(session, mutate)
}
