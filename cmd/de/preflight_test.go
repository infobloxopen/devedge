package main

import (
	"strings"
	"testing"
)

func TestRequireTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tools     []string
		wantErr   bool
		wantInMsg []string // substrings that must appear in the error message
	}{
		{
			name:    "present tool returns nil",
			tools:   []string{"go"},
			wantErr: false,
		},
		{
			name:      "missing tool returns error with its name",
			tools:     []string{"definitely-not-a-real-binary-xyz"},
			wantErr:   true,
			wantInMsg: []string{"definitely-not-a-real-binary-xyz"},
		},
		{
			name:      "mix: only the missing tool is reported",
			tools:     []string{"go", "definitely-not-a-real-binary-xyz"},
			wantErr:   true,
			wantInMsg: []string{"definitely-not-a-real-binary-xyz"},
		},
		{
			name:      "multiple missing tools all appear in the error",
			tools:     []string{"definitely-not-a-real-binary-xyz", "another-fake-tool-abc"},
			wantErr:   true,
			wantInMsg: []string{"definitely-not-a-real-binary-xyz", "another-fake-tool-abc"},
		},
		{
			name:    "no tools requested returns nil",
			tools:   nil,
			wantErr: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := requireTools(tc.tools...)
			if tc.wantErr && err == nil {
				t.Fatal("expected an error but got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil but got error: %v", err)
			}
			for _, sub := range tc.wantInMsg {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error message %q does not contain %q", err.Error(), sub)
				}
			}
		})
	}
}
