//go:build darwin

package main

import (
	"errors"

	"minecraft-go/internal/core"
)

var errBlockTargetUnknown = errors.New("mcgo: block target path is unknown")

type blockTarget struct {
	Position core.BlockPos
	Name     string
}

func (a *application) currentBlockTarget() (blockTarget, bool) {
	if _, ready := a.predictor.State(); !ready || a.inventoryOpen || a.containerOpen() ||
		(a.panel != nil && a.panel.visible) {
		return blockTarget{}, false
	}

	var targetID core.BlockID
	hit, found, err := core.RaycastBlocks(
		a.camera.Pos,
		a.camera.Forward(),
		6,
		func(position core.BlockPos) (bool, error) {
			id, loaded := a.mirror.BlockAt(core.Overworld, position)
			chunk, _ := a.mirror.Chunk(core.Overworld, position.Chunk())
			if !loaded || (chunk != nil && chunk.Desynced) || !core.RegisteredBlock(id) {
				return false, errBlockTargetUnknown
			}
			targetID = id
			return id != core.AirID, nil
		},
	)
	if err != nil || !found {
		return blockTarget{}, false
	}
	name, ok := core.BlockDisplayName(targetID)
	if !ok || name == "" {
		return blockTarget{}, false
	}
	return blockTarget{Position: hit.Block, Name: name}, true
}
