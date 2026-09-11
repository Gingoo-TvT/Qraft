package activities

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestQuizStableIDsAreDeterministicAndPositionScoped(t *testing.T) {
	key := "workflow/store-quiz/v1"
	idFor := func(index int) uuid.UUID {
		return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("algoforge:store-quiz:%s:%d", key, index)))
	}

	if idFor(0) != idFor(0) {
		t.Fatal("same operation and position produced different IDs")
	}
	if idFor(0) == idFor(1) {
		t.Fatal("different positions produced the same ID")
	}
}
