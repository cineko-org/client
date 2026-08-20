package cgv

import (
	"testing"
)

func TestDateButtonMatchesSemanticOrderAndSpacing(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  bool
	}{
		{name: "compact", label: "오늘12", want: true},
		{name: "spaced", label: "오늘 12", want: true},
		{name: "wide spacing", label: "오늘     12", want: true},
		{name: "reversed spaced", label: "12 오늘", want: true},
		{name: "reversed compact", label: "12오늘", want: true},
		{name: "punctuation", label: "오늘 · 12", want: true},
		{name: "weekday fallback", label: "수12", want: true},
		{name: "wrong day", label: "오늘13", want: false},
		{name: "unrelated text", label: "오늘 상영 12", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dateButtonMatches(test.label, 12, []string{"오늘", "수"}); got != test.want {
				t.Fatalf("dateButtonMatches(%q) = %t, want %t", test.label, got, test.want)
			}
		})
	}
}
