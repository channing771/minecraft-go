package client

import "minecraft-go/internal/core"

type Actions struct {
	Break         bool
	Place         bool
	SelectedBlock core.BlockID
}

type InputState struct {
	primaryDown   bool
	secondaryDown bool
	selectedBlock core.BlockID
}

func (state *InputState) Update(primary, secondary bool, number int) Actions {
	if state.selectedBlock == core.AirID {
		state.selectedBlock = core.StoneID
	}
	switch number {
	case 1:
		state.selectedBlock = core.StoneID
	case 2:
		state.selectedBlock = core.DirtID
	case 3:
		state.selectedBlock = core.GrassID
	}
	actions := Actions{
		Break:         primary && !state.primaryDown,
		Place:         secondary && !state.secondaryDown,
		SelectedBlock: state.selectedBlock,
	}
	state.primaryDown = primary
	state.secondaryDown = secondary
	return actions
}
