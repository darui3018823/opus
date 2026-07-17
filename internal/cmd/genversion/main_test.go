package main

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      string
		wantParts [3]int
		wantErr   bool
	}{
		{name: "release", input: "1.2.0\n", want: "1.2.0", wantParts: [3]int{1, 2, 0}},
		{name: "zero", input: "0.0.0", want: "0.0.0", wantParts: [3]int{}},
		{name: "missing component", input: "1.2", wantErr: true},
		{name: "leading v", input: "v1.2.0", wantErr: true},
		{name: "prerelease", input: "1.2.0-rc.1", wantErr: true},
		{name: "leading zero", input: "1.02.0", wantErr: true},
		{name: "signed component", input: "1.+2.0", wantErr: true},
		{name: "empty component", input: "1..0", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, parts, err := parseVersion(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseVersion(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
			if err == nil && (got != tc.want || parts != tc.wantParts) {
				t.Fatalf("parseVersion(%q) = (%q, %v), want (%q, %v)", tc.input, got, parts, tc.want, tc.wantParts)
			}
		})
	}
}
