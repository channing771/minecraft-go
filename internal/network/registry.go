package network

// clientPacketID returns the frozen V1 ID for packet in state.
func clientPacketID(state State, packet ClientPacket) (uint32, bool) {
	switch state {
	case StateHandshake:
		_, ok := packet.(ClientHello)
		return 0, ok
	case StateLogin:
		_, ok := packet.(LoginStart)
		return 0, ok
	case StatePlay:
		switch packet.(type) {
		case PlayerInput:
			return 0, true
		case PlaceBlock:
			return 2, true
		case RequestChunkResync:
			return 3, true
		case KeepAliveReply:
			return 4, true
		case SelectHotbar:
			return 5, true
		case MoveInventoryStack:
			return 6, true
		case CraftRecipe:
			return 7, true
		case OpenFurnace:
			return 8, true
		case MoveFurnaceStack:
			return 9, true
		case CloseFurnace:
			return 10, true
		case DropSelectedItem:
			return 11, true
		}
	}
	return 0, false
}

// clientPacketForID returns an empty packet of the frozen V1 type.
func clientPacketForID(state State, id uint32) (ClientPacket, bool) {
	switch state {
	case StateHandshake:
		if id == 0 {
			return ClientHello{}, true
		}
	case StateLogin:
		if id == 0 {
			return LoginStart{}, true
		}
	case StatePlay:
		switch id {
		case 0:
			return PlayerInput{}, true
		case 2:
			return PlaceBlock{}, true
		case 3:
			return RequestChunkResync{}, true
		case 4:
			return KeepAliveReply{}, true
		case 5:
			return SelectHotbar{}, true
		case 6:
			return MoveInventoryStack{}, true
		case 7:
			return CraftRecipe{}, true
		case 8:
			return OpenFurnace{}, true
		case 9:
			return MoveFurnaceStack{}, true
		case 10:
			return CloseFurnace{}, true
		case 11:
			return DropSelectedItem{}, true
		}
	}
	return nil, false
}

// serverPacketID returns the frozen V1 ID for packet in state.
func serverPacketID(state State, packet ServerPacket) (uint32, bool) {
	switch state {
	case StateHandshake:
		switch packet.(type) {
		case ServerHello:
			return 0, true
		case HandshakeReject:
			return 1, true
		}
	case StateLogin:
		switch packet.(type) {
		case LoginSuccess:
			return 0, true
		case LoginReject:
			return 1, true
		}
	case StatePlay:
		switch packet.(type) {
		case ChunkSnapshot:
			return 0, true
		case BlockChanges:
			return 1, true
		case ForgetChunks:
			return 2, true
		case PlayerState:
			return 3, true
		case CommandRejected:
			return 4, true
		case KeepAlive:
			return 5, true
		case Disconnect:
			return 6, true
		case RemotePlayerSpawn:
			return 7, true
		case RemotePlayerDespawn:
			return 8, true
		case RemotePlayerStates:
			return 9, true
		case InventoryState:
			return 10, true
		case ItemDropUpserts:
			return 11, true
		case ItemDropRemoves:
			return 12, true
		case FurnaceState:
			return 13, true
		case FurnaceClosed:
			return 14, true
		}
	}
	return 0, false
}

// serverPacketForID returns an empty packet of the frozen V1 type.
func serverPacketForID(state State, id uint32) (ServerPacket, bool) {
	switch state {
	case StateHandshake:
		switch id {
		case 0:
			return ServerHello{}, true
		case 1:
			return HandshakeReject{}, true
		}
	case StateLogin:
		switch id {
		case 0:
			return LoginSuccess{}, true
		case 1:
			return LoginReject{}, true
		}
	case StatePlay:
		switch id {
		case 0:
			return ChunkSnapshot{}, true
		case 1:
			return BlockChanges{}, true
		case 2:
			return ForgetChunks{}, true
		case 3:
			return PlayerState{}, true
		case 4:
			return CommandRejected{}, true
		case 5:
			return KeepAlive{}, true
		case 6:
			return Disconnect{}, true
		case 7:
			return RemotePlayerSpawn{}, true
		case 8:
			return RemotePlayerDespawn{}, true
		case 9:
			return RemotePlayerStates{}, true
		case 10:
			return InventoryState{}, true
		case 11:
			return ItemDropUpserts{}, true
		case 12:
			return ItemDropRemoves{}, true
		case 13:
			return FurnaceState{}, true
		case 14:
			return FurnaceClosed{}, true
		}
	}
	return nil, false
}

func commandRejectReasonID(reason RejectReason) (uint8, bool) {
	switch reason {
	case RejectInvalidRay:
		return 1, true
	case RejectNoTarget:
		return 2, true
	case RejectChunkNotReady:
		return 3, true
	case RejectProtectedBlock:
		return 4, true
	case RejectInvalidBlock:
		return 5, true
	case RejectOccupied:
		return 6, true
	case RejectInvalidInput:
		return 7, true
	case RejectPlayerNotReady:
		return 8, true
	case RejectInvalidSlot:
		return 9, true
	case RejectHotbarFull:
		return 10, true
	case RejectDropCapacity:
		return 11, true
	case RejectFurnaceCapacity:
		return 12, true
	default:
		return 0, false
	}
}

func commandRejectReasonForID(id uint8) (RejectReason, bool) {
	switch id {
	case 1:
		return RejectInvalidRay, true
	case 2:
		return RejectNoTarget, true
	case 3:
		return RejectChunkNotReady, true
	case 4:
		return RejectProtectedBlock, true
	case 5:
		return RejectInvalidBlock, true
	case 6:
		return RejectOccupied, true
	case 7:
		return RejectInvalidInput, true
	case 8:
		return RejectPlayerNotReady, true
	case 9:
		return RejectInvalidSlot, true
	case 10:
		return RejectHotbarFull, true
	case 11:
		return RejectDropCapacity, true
	case 12:
		return RejectFurnaceCapacity, true
	default:
		return "", false
	}
}
