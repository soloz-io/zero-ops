package main

import (
	"testing"
	"time"
)

func TestSaveStateViewInProgressAndFinished(t *testing.T) {
	running := (&saveState{Trigger: "periodic", Since: time.Now().Add(-90 * time.Second)}).view()
	if running["trigger"] != "periodic" || running["elapsedSeconds"].(int) < 89 || running["outcome"] != nil {
		t.Fatalf("in-progress view = %v", running)
	}
	done := (&saveState{Trigger: "on-demand", Since: time.Now(), Finished: time.Now(), Outcome: "saved", CheckpointID: "c1"}).view()
	if done["outcome"] != "saved" || done["checkpointId"] != "c1" || done["elapsedSeconds"] != nil {
		t.Fatalf("finished view = %v", done)
	}
}
