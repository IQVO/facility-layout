package kafka_test

import (
	"testing"

	"github.com/claudioed/facility-layout/internal/domain/shared"
)

// mustGLN parses a known-valid GS1 Global Location Number for a wire case.
func mustGLN(t *testing.T, value string) shared.GLN {
	t.Helper()
	gln, err := shared.ParseGLN(value)
	if err != nil {
		t.Fatalf("ParseGLN(%q): %v", value, err)
	}
	return gln
}

// mustYardCode is the location code of a yard spot used by the wire cases.
func mustYardCode(t *testing.T) shared.LocationCode {
	t.Helper()
	code, err := shared.ParseLocationCode("WH1-YARD-NORTH-Y01-01-01-A")
	if err != nil {
		t.Fatalf("ParseLocationCode: %v", err)
	}
	return code
}
