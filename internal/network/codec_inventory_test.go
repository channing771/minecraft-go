package network

import (
	"encoding/hex"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestProtocolV11InventoryCarriesWornToolDurability(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 73}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: 149}
	packet := InventoryState{Inventory: inventory}
	packetID, payload, err := encodeServerControlPayload(StatePlay, packet)
	if err != nil {
		t.Fatalf("编码磨损工具背包: %v", err)
	}
	if packetID != 10 || len(payload) != 1+core.InventorySlots*5 {
		t.Fatalf("InventoryState id=%d payload=%d，想要 id=10 payload=181", packetID, len(payload))
	}
	decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil || decoded != packet {
		t.Fatalf("磨损工具背包往返 = %+v, %v，想要 %+v", decoded, err, packet)
	}
}

func TestProtocolV15InventoryCarriesLightBlockItem(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Selected = 3
	inventory.Hotbar.Slots[3] = core.ItemStack{Item: core.ItemLightBlock, Count: 17}
	packet := InventoryState{Inventory: inventory}

	packetID, payload, err := encodeServerControlPayload(StatePlay, packet)
	if err != nil {
		t.Fatalf("编码发光块物品背包: %v", err)
	}
	if packetID != 10 || len(payload) != 181 {
		t.Fatalf("InventoryState id=%d payload=%d，想要 id=10 payload=181", packetID, len(payload))
	}
	const lightBlockOffset = 16
	if got := hex.EncodeToString(payload[lightBlockOffset : lightBlockOffset+5]); got != "0f00110000" {
		t.Fatalf("发光块物品 wire=%s，想要 0f00110000", got)
	}

	decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil {
		t.Fatalf("解码发光块物品背包: %v", err)
	}
	got, ok := decoded.(InventoryState)
	if !ok {
		t.Fatalf("发光块物品背包解码类型=%T，想要 InventoryState", decoded)
	}
	if got.Inventory.Hotbar.Selected != packet.Inventory.Hotbar.Selected ||
		got.Inventory.Hotbar.Slots != packet.Inventory.Hotbar.Slots ||
		got.Inventory.Backpack != packet.Inventory.Backpack {
		t.Fatalf("发光块物品背包往返=%+v，想要 %+v", got, packet)
	}
}
