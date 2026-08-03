package server

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

// publishDrops 把该会话兴趣范围内的当前掉落物与已发布镜像做有界差分：
// 先按 ID 顺序发送 remove，再发送新增或状态变化的 upsert，每批最多 32 项。
// 只有 enqueue 成功才更新镜像，失败沿用现有慢会话关闭流程。
func (server *Server) publishDrops(current *session, tick uint64) bool {
	// 受信观察者不是玩家，没有掉落物兴趣范围。
	if server.engine == nil || current == server.trustedObserver {
		return true
	}
	current.dropScratch = server.engine.AppendSessionDrops(current.id, current.dropScratch[:0])

	removes := current.dropRemoveScratch[:0]
	for id := range current.publishedDrops {
		if !containsDropID(current.dropScratch, id) {
			removes = append(removes, id)
		}
	}
	sortDropIDs(removes)
	current.dropRemoveScratch = removes

	upserts := current.dropUpsertScratch[:0]
	for _, drop := range current.dropScratch {
		published, known := current.publishedDrops[drop.ID]
		if known && published == drop {
			continue
		}
		upserts = append(upserts, network.ItemDrop{
			ID: drop.ID, BlockIndex: drop.BlockIndex, Item: drop.Item, Count: drop.Count,
		})
	}
	current.dropUpsertScratch = upserts

	for start := 0; start < len(removes); start += network.MaxItemDropBatch {
		end := min(start+network.MaxItemDropBatch, len(removes))
		// 发送成功后消息与其切片视为不可变，因此每批复制出独立底层数组。
		batch := append([]core.DropID(nil), removes[start:end]...)
		if !current.enqueue(network.ItemDropRemoves{ServerTick: tick, IDs: batch}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		for _, id := range removes[start:end] {
			delete(current.publishedDrops, id)
		}
	}
	for start := 0; start < len(upserts); start += network.MaxItemDropBatch {
		end := min(start+network.MaxItemDropBatch, len(upserts))
		batch := append([]network.ItemDrop(nil), upserts[start:end]...)
		if !current.enqueue(network.ItemDropUpserts{ServerTick: tick, Drops: batch}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		for _, drop := range upserts[start:end] {
			current.publishedDrops[drop.ID] = sim.DropSnapshot{
				ID: drop.ID, BlockIndex: drop.BlockIndex, Item: drop.Item, Count: drop.Count,
			}
		}
	}
	return true
}

func containsDropID(drops []sim.DropSnapshot, id core.DropID) bool {
	for _, drop := range drops {
		if drop.ID == id {
			return true
		}
	}
	return false
}

func sortDropIDs(ids []core.DropID) {
	for index := 1; index < len(ids); index++ {
		current := ids[index]
		position := index
		for position > 0 && ids[position-1].Compare(current) > 0 {
			ids[position] = ids[position-1]
			position--
		}
		ids[position] = current
	}
}

// CloneReadyChunkForTest 复制一个已 Ready 的权威区块，仅供纵向测试断言状态。
func (server *Server) CloneReadyChunkForTest(
	key core.ChunkKey,
) (*world.Chunk, uint64, bool) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	return server.engine.CloneReadyChunk(key)
}

// SetChunkDropForTest 直接写入权威区块的掉落物槽，仅供纵向测试构造固定场景。
func (server *Server) SetChunkDropForTest(
	key core.ChunkKey,
	slot int,
	value world.DropSlot,
) {
	server.stepMu.Lock()
	defer server.stepMu.Unlock()
	server.engine.SetChunkDropForTest(key, slot, value)
}
