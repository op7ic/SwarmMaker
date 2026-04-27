// state.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// OODA state transition definitions.
// Defines the Observe/Orient/Decide/Act/Complete/Failed state set and the
// allowed transitions between them. AllowedNextStates returns the valid
// transitions from any given state, used by the output renderers to emit
// OODA agent files in the correct structure.


package ooda

import (
	"errors"
	"fmt"
)

type State string

const (
	StateObserve  State = "observe"
	StateOrient   State = "orient"
	StateDecide   State = "decide"
	StateAct      State = "act"
	StateComplete State = "complete"
	StateFailed   State = "failed"
)

var ErrUnknownState = errors.New("unknown ooda state")

var transitionTable = map[State][]State{
	StateObserve:  {StateOrient, StateFailed},
	StateOrient:   {StateDecide, StateFailed},
	StateDecide:   {StateAct, StateFailed},
	StateAct:      {StateComplete, StateFailed},
	StateComplete: {},
	StateFailed:   {},
}

func AllowedNextStates(state State) ([]State, error) {
	if err := validateState(state); err != nil {
		return nil, err
	}
	next := transitionTable[state]
	out := make([]State, len(next))
	copy(out, next)
	return out, nil
}

func validateState(state State) error {
	if _, ok := transitionTable[state]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownState, state)
	}
	return nil
}

