package queue

import (
	"encoding/json"
	"testing"
)

func TestTopicForLanguage(t *testing.T) {
	tests := []struct {
		lang string
		want string
	}{
		{"cpp", TopicFast},
		{"c", TopicFast},
		{"go", TopicFast},
		{"rust", TopicFast},
		{"python", TopicSlow},
		{"python3", TopicSlow},
		{"java", TopicSlow},
		{"javascript", TopicSlow},
		{"ruby", TopicSlow},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			got := topicForLanguage(tt.lang)
			if got != tt.want {
				t.Errorf("topicForLanguage(%q) = %q, want %q", tt.lang, got, tt.want)
			}
		})
	}
}

func TestJudgeTaskMarshal(t *testing.T) {
	contestID := uint(7)
	task := JudgeTask{
		SubmissionID: 42,
		ProblemID:    1,
		UserID:       3,
		ContestID:    &contestID,
		Language:     "cpp",
		Code:         "#include <iostream>\nint main() { return 0; }",
		TimeLimit:    1000,
		MemoryLimit:  128,
		RetryCount:   0,
		TraceParent:  "00-abcdef1234567890abcdef1234567890-1234567890abcdef-01",
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded JudgeTask
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.SubmissionID != 42 {
		t.Errorf("submission_id = %d, want 42", decoded.SubmissionID)
	}
	if decoded.ProblemID != 1 {
		t.Errorf("problem_id = %d, want 1", decoded.ProblemID)
	}
	if decoded.UserID != 3 {
		t.Errorf("user_id = %d, want 3", decoded.UserID)
	}
	if decoded.ContestID == nil || *decoded.ContestID != 7 {
		t.Error("contest_id mismatch")
	}
	if decoded.Language != "cpp" {
		t.Errorf("language = %q, want cpp", decoded.Language)
	}
	if decoded.TimeLimit != 1000 {
		t.Errorf("time_limit = %d, want 1000", decoded.TimeLimit)
	}
	if decoded.MemoryLimit != 128 {
		t.Errorf("memory_limit = %d, want 128", decoded.MemoryLimit)
	}
	if decoded.TraceParent != "00-abcdef1234567890abcdef1234567890-1234567890abcdef-01" {
		t.Errorf("trace_parent = %q", decoded.TraceParent)
	}
}

func TestJudgeTaskMarshalWithoutContest(t *testing.T) {
	task := JudgeTask{
		SubmissionID: 1,
		ProblemID:    2,
		UserID:       3,
		ContestID:    nil,
		Language:     "python",
		Code:         "print(1)",
		TimeLimit:    500,
		MemoryLimit:  64,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded JudgeTask
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.ContestID != nil {
		t.Error("contest_id should be nil")
	}
}

func TestMaxRetries(t *testing.T) {
	if MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", MaxRetries)
	}
}

func TestTopicFastSlow(t *testing.T) {
	if TopicFast != "judge_tasks_fast" {
		t.Errorf("TopicFast = %q", TopicFast)
	}
	if TopicSlow != "judge_tasks_slow" {
		t.Errorf("TopicSlow = %q", TopicSlow)
	}
}

func TestStatsDefault(t *testing.T) {
	backend, bufLen, nsqOK, workerCount := Stats()

	// backend may be empty if no queue backend is initialized — that's OK.
	// Just verify Stats() doesn't panic.
	if workerCount != 4 {
		t.Errorf("workerCount = %d, want 4", workerCount)
	}

	_ = backend
	_ = bufLen
	_ = nsqOK
}
