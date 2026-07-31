package citation

import "testing"

func TestFormatLocation(t *testing.T) {
	tests := []struct {
		docType string
		start   int
		end     int
		want    string
	}{
		{"md", 12, 40, "lines 12-40"},
		{"txt", 3, 5, "lines 3-5"},
		{"obsidian", 1, 1, "lines 1"},
		{"md", 0, 0, ""},
	}
	for _, tc := range tests {
		got := FormatLocation(tc.docType, tc.start, tc.end)
		if got != tc.want {
			t.Errorf("FormatLocation(%q, %d, %d) = %q, want %q", tc.docType, tc.start, tc.end, got, tc.want)
		}
	}
}

func TestFormatSource(t *testing.T) {
	got := FormatSource("/docs/billing.md", "md", 12, 40)
	want := "/docs/billing.md (lines 12-40)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
