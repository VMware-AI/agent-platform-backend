package graph

import "testing"

func TestSelectPrimaryModel(t *testing.T) {
	tests := []struct {
		name      string
		models    []string
		want      string
		wantFound bool
	}{
		{
			name:      "picks first non-empty model",
			models:    []string{"", "  ", "qw35", "qwen-coder"},
			want:      "qw35",
			wantFound: true,
		},
		{
			name:      "rejects empty model list",
			models:    nil,
			want:      "",
			wantFound: false,
		},
		{
			name:      "rejects blank-only model list",
			models:    []string{"", "   "},
			want:      "",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := selectPrimaryModel(tt.models)
			if got != tt.want || ok != tt.wantFound {
				t.Fatalf("selectPrimaryModel(%v) = (%q, %v), want (%q, %v)", tt.models, got, ok, tt.want, tt.wantFound)
			}
		})
	}
}
