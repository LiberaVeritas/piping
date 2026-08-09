package pdf

import "testing"

func TestIsPDF(t *testing.T) {
	cases := []struct {
		name string
		doc  []byte
		want bool
	}{
		{"typical header", []byte("%PDF-1.4\n1 0 obj\n"), true},
		{"header alone", []byte("%PDF-"), true},
		{"later version", []byte("%PDF-2.0"), true},
		{"truncated magic", []byte("%PDF"), false},
		{"empty", []byte{}, false},
		{"nil", nil, false},
		{"leading whitespace", []byte(" %PDF-1.4"), false},
		{"leading newline", []byte("\n%PDF-1.4"), false},
		{"postscript", []byte("%!PS-Adobe-3.0"), false},
		{"magic not at the start", []byte("GIF89a%PDF-1.4"), false},
		{"plain text", []byte("just some bytes"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPDF(tc.doc); got != tc.want {
				t.Errorf("IsPDF(%q) = %v, want %v", tc.doc, got, tc.want)
			}
		})
	}
}
