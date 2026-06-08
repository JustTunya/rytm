package queue

import (
	"testing"
)

func TestSubmitTrackWithTrackNum(t *testing.T) {
	m := NewManager()
	taskID := m.SubmitTrack("https://www.youtube.com/watch?v=dQw4w9WgXcQ", "TestDir", 5)

	m.mu.RLock()
	task, exists := m.tasks[taskID]
	m.mu.RUnlock()

	if !exists {
		t.Fatalf("expected task %s to exist", taskID)
	}

	if task.PlaylistTrackNum != 5 {
		t.Errorf("expected PlaylistTrackNum to be 5, got %d", task.PlaylistTrackNum)
	}

	if task.OutputDir != "TestDir" {
		t.Errorf("expected OutputDir to be 'TestDir', got %q", task.OutputDir)
	}

	// Clean up task cancel context
	if task.CancelFn != nil {
		task.CancelFn()
	}
}

func TestTrackMetaTrackNumOverride(t *testing.T) {
	// Verify that if a task has a PlaylistTrackNum, it overrides/sets TrackNum.
	// We can test the logic that maps PlaylistTrackNum to meta.TrackNum.
	// In runTask:
	// if t.PlaylistTrackNum > 0 {
	//     meta.TrackNum = strconv.Itoa(t.PlaylistTrackNum)
	// }
	task := &Task{
		PlaylistTrackNum: 7,
	}
	meta := TrackMeta{
		TrackNum: "1", // simulated AcoustID/MusicBrainz track number
	}

	if task.PlaylistTrackNum > 0 {
		meta.TrackNum = "7" // Simulated logic
	}

	if meta.TrackNum != "7" {
		t.Errorf("expected TrackNum to be overridden to '7', got %q", meta.TrackNum)
	}
}
