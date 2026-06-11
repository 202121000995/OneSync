package progress

import "testing"

func TestValidateRejectsInvalidProgress(t *testing.T) {
	tests := []Snapshot{
		{TotalFiles: -1},
		{TotalFiles: 1, CompletedFiles: -1},
		{TotalFiles: 1, CompletedFiles: 2},
		{Stage: "unknown"},
		{TotalFiles: 1, CurrentPath: "bad\x00path"},
	}
	for _, test := range tests {
		if err := Validate(test); err == nil {
			t.Fatalf("Validate(%+v) error = nil", test)
		}
	}
}

func TestValidateAcceptsCurrentPath(t *testing.T) {
	if err := Validate(Snapshot{
		TotalFiles:     2,
		CompletedFiles: 1,
		Stage:          StageTransfer,
		CurrentPath:    "folder/file.txt",
	}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
