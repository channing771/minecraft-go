package server_test

import (
	"fmt"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

func newMemoryAttachedWorldForExternalTest(config server.Config, endpoint network.ServerEndpoint, generator server.Generator) *server.Server {
	return newMemoryAttachedWorldWithHotbar(config, endpoint, generator, core.Hotbar{})
}

// newMemoryAttachedWorldWithHotbar 让放置类纵向测试可以从已确认的快捷栏起步；
// M4B 的挖掘只产生地面掉落物，不再即时补充快捷栏。
func newMemoryAttachedWorldWithHotbar(
	config server.Config,
	endpoint network.ServerEndpoint,
	generator server.Generator,
	hotbar core.Hotbar,
) *server.Server {
	running := server.NewWorld(config, generator, storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: config.Seed, SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}))
	if config.TrustedObserver {
		if err := running.AttachTrustedObserver(endpoint); err != nil {
			panic(err)
		}
		return running
	}
	restore := sim.PlayerRestore{
		SpawnDimension: config.SpawnDimension,
		SpawnAnchor:    config.SpawnAnchor,
		Inventory:      core.Inventory{Hotbar: hotbar},
	}
	if _, err := running.AttachSession(externalSessionSpec(1, 1, endpoint, restore)); err != nil {
		panic(err)
	}
	return running
}

// stockedTestHotbar 返回栏位 0 装满该物品的快捷栏。
func stockedTestHotbar(item core.ItemID) core.Hotbar {
	var hotbar core.Hotbar
	hotbar.Slots[0] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	return hotbar
}

func playerStateForExternalTest(running *server.Server) (sim.PlayerUpdate, bool) {
	return running.PlayerStateFor(1)
}

func newAttachedPersistentWorldForExternalTest(config server.Config, endpoint network.ServerEndpoint, generator server.Generator, store storage.Store) *server.Server {
	running := server.NewWorld(config, generator, store)
	if config.TrustedObserver {
		if err := running.AttachTrustedObserver(endpoint); err != nil {
			panic(err)
		}
		return running
	}
	restore := sim.PlayerRestore{
		SpawnDimension: config.SpawnDimension,
		SpawnAnchor:    config.SpawnAnchor,
		Inventory:      core.Inventory{Hotbar: persistentTestHotbar},
	}
	if _, err := running.AttachSession(externalSessionSpec(1, 1, endpoint, restore)); err != nil {
		panic(err)
	}
	return running
}

// persistentTestHotbar 让持久化纵向脚本可以放置方块；挖掘只产生地面掉落物。
var persistentTestHotbar = stockedTestHotbar(core.ItemStone)

func externalSessionSpec(
	id sim.SessionID,
	generation uint64,
	endpoint network.ServerEndpoint,
	restore sim.PlayerRestore,
) server.SessionSpec {
	return server.SessionSpec{
		ID:          id,
		Generation:  generation,
		PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, byte(id)},
		DisplayName: fmt.Sprintf("Player-%d", id),
		Endpoint:    endpoint,
		Restore:     restore,
	}
}
