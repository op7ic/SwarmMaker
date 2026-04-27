// state_test.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Tests for OODA state transitions.
// Verifies determinism (same input produces same output) and slice
// independence (returned slices don't alias internal state).


package ooda_test

import (
	"reflect"
	"testing"

	"github.com/op7ic/swarmmaker/internal/ooda"
)

func TestAllowedNextStatesAreDeterministic(t *testing.T) {
	t.Parallel()

	first, err := ooda.AllowedNextStates(ooda.StateObserve)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	second, err := ooda.AllowedNextStates(ooda.StateObserve)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expected deterministic next states, got %v and %v", first, second)
	}

	first[0] = ooda.StateAct
	third, err := ooda.AllowedNextStates(ooda.StateObserve)
	if err != nil {
		t.Fatalf("third call failed: %v", err)
	}
	if !reflect.DeepEqual(second, third) {
		t.Fatalf("expected returned slices to be independent copies, got %v and %v", second, third)
	}
}

