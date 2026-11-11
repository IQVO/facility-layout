package shared_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// Each valid GLN below was computed independently from the GS1 rule: weights
// 3,1,3,1,... from the RIGHT over the first twelve digits, check digit =
// (10 - sum mod 10) mod 10. 4006381333931, 9780201379624 and 5901234123457 are
// widely published GS1 examples.
var validGLNs = []string{
	"4006381333931",
	"5412345000013",
	"4012345000009",
	"9780201379624",
	"5901234123457",
	"6291041500213",
	"7601234567890",
	"1234567890128",
	"8712345678906",
	"0000000000000",
}

func TestParseGLNAcceptsCorrectCheckDigits(t *testing.T) {
	for _, value := range validGLNs {
		t.Run(value, func(t *testing.T) {
			got, err := shared.ParseGLN(value)
			if err != nil {
				t.Fatalf("ParseGLN(%q): %v", value, err)
			}
			if got.String() != value || got.IsZero() {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestParseGLNRejectsEveryWrongCheckDigit(t *testing.T) {
	for _, value := range validGLNs {
		body, check := value[:12], int(value[12]-'0')
		for wrong := 0; wrong <= 9; wrong++ {
			if wrong == check {
				continue
			}
			candidate := body + string(rune('0'+wrong))
			if _, err := shared.ParseGLN(candidate); !errors.Is(err, shared.ErrInvalidGLN) {
				t.Errorf("ParseGLN(%q) err = %v, want ErrInvalidGLN", candidate, err)
			}
		}
	}
}

// A transposition of two adjacent digits with different weights changes the
// weighted sum, so the check digit no longer matches.
func TestParseGLNCatchesAdjacentTransposition(t *testing.T) {
	if _, err := shared.ParseGLN("4060381333931"); !errors.Is(err, shared.ErrInvalidGLN) {
		t.Fatalf("err = %v, want ErrInvalidGLN", err)
	}
}

func TestParseGLNRejectsMalformedShapes(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"too short":     "400638133393",
		"too long":      "40063813339311",
		"letters":       "40063813339A1",
		"spaces":        "4006381 33931",
		"unicode digit": "400638133393١",
		"hyphenated":    "4-006381-333931",
		"leading plus":  "+006381333931",
		// ':' is '0'+10, so it adds a multiple of ten to the weighted sum and
		// the check digit still "matches": only the digit check refuses it.
		"colon posing as a zero": "4:06381333931",
		"twelve digits only":     strings.Repeat("1", 12),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := shared.ParseGLN(value); !errors.Is(err, shared.ErrInvalidGLN) {
				t.Fatalf("ParseGLN(%q) err = %v, want ErrInvalidGLN", value, err)
			}
		})
	}
}

func TestGLNCheckDigit(t *testing.T) {
	tests := []struct {
		body string
		want int
	}{
		{"400638133393", 1},
		{"541234500001", 3},
		{"401234500000", 9},
		{"978020137962", 4},
		{"590123412345", 7},
		{"629104150021", 3},
		{"760123456789", 0},
		{"123456789012", 8},
		{"000000000001", 7}, // a single rightmost 1 weighs 3: (10-3)%10
		{"000000000010", 9}, // the next digit weighs 1: (10-1)%10
	}
	for _, tc := range tests {
		if got := shared.GLNCheckDigit(tc.body); got != tc.want {
			t.Errorf("GLNCheckDigit(%q) = %d, want %d", tc.body, got, tc.want)
		}
	}
}

func TestGLNZeroValue(t *testing.T) {
	var none shared.GLN
	if !none.IsZero() || none.String() != "" {
		t.Fatalf("zero GLN = %q", none)
	}
}
