package handler

import "testing"

func TestNormalizeKnowledgeSlugPreservesHierarchy(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain slug",
			in:   "Core Loop Values",
			want: "core-loop-values",
		},
		{
			name: "nested slug",
			in:   "Design/Core Loop Values",
			want: "design/core-loop-values",
		},
		{
			name: "collapses empty segments",
			in:   " / Design // Core Loop / ",
			want: "design/core-loop",
		},
		{
			name: "normalizes punctuation inside segments",
			in:   "01-Overview/Pitch & World",
			want: "01-overview/pitch-world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeKnowledgeSlug(tt.in); got != tt.want {
				t.Fatalf("normalizeKnowledgeSlug(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
