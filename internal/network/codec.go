package network

import (
	"errors"
	"fmt"
)

const MaxSmallPayload = 64 << 10

var (
	errInvalidDimension = errors.New("network: dimension is not overworld")
	errInvalidCount     = errors.New("network: packet count is outside 1..4096")
	errCountShortInput  = errors.New("network: packet count exceeds remaining payload")
)

func finishEncode(direction string, state State, packetID uint32, e byteEncoder) (uint32, []byte, error) {
	if e.err != nil {
		return 0, nil, codecError(direction, state, packetID, e.err)
	}
	if err := checkSmallPayload(e.data); err != nil {
		return 0, nil, codecError(direction, state, packetID, err)
	}
	return packetID, e.data, nil
}

func checkSmallPayload(payload []byte) error {
	if len(payload) > MaxSmallPayload {
		return errors.New("network: payload exceeds 64 KiB")
	}
	return nil
}

var (
	errUnknownPacketID   = errors.New("network: unknown packet ID")
	errSnapshotDelegated = errors.New("network: Play/S→C/ID 0 ChunkSnapshot is handled by Task 5")
)

func codecError(direction string, state State, packetID uint32, err error) error {
	return fmt.Errorf("network: %s state=%d packetID=%d: %w", direction, state, packetID, err)
}
