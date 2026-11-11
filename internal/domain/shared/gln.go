package shared

import "errors"

// ErrInvalidGLN is returned when a Global Location Number is not exactly 13
// ASCII digits or its last digit is not the GS1 mod-10 check digit of the
// first twelve (ADR-0045).
var ErrInvalidGLN = errors.New("gln must be 13 digits ending in the GS1 mod-10 check digit")

// glnLength is the number of digits in a GS1 Global Location Number.
const glnLength = 13

// GLN is a GS1 Global Location Number: the standard identity partners use for
// a physical location. It is OPTIONAL on a Site, a Zone and a LocationSlot;
// the zero value ("") means none was ever recorded, which is every location
// registered before ADR-0045. The optional GLN extension component is not
// modelled.
type GLN string

// String returns the 13-digit form.
func (g GLN) String() string { return string(g) }

// IsZero reports whether no GLN is recorded.
func (g GLN) IsZero() bool { return g == "" }

// ParseGLN validates and converts the string form: exactly 13 digits whose
// last digit is the GS1 check digit of the first twelve. The empty string is
// NOT valid here: "absent" is represented by not calling the parser.
func ParseGLN(value string) (GLN, error) {
	if len(value) != glnLength {
		return "", ErrInvalidGLN
	}
	for i := 0; i < glnLength; i++ {
		if value[i] < '0' || value[i] > '9' {
			return "", ErrInvalidGLN
		}
	}
	if GLNCheckDigit(value[:glnLength-1]) != int(value[glnLength-1]-'0') {
		return "", ErrInvalidGLN
	}
	return GLN(value), nil
}

// GLNCheckDigit computes the GS1 mod-10 check digit of body, the digits that
// precede it (twelve for a GLN). Starting from the rightmost digit of body
// the weights alternate 3, 1, 3, 1, ...; the check digit is the amount that
// rounds the weighted sum up to the next multiple of ten. body must contain
// digits only; ParseGLN guarantees that before calling it.
func GLNCheckDigit(body string) int {
	sum := 0
	weight := 3
	for i := len(body) - 1; i >= 0; i-- {
		sum += int(body[i]-'0') * weight
		weight = 4 - weight // 3 <-> 1
	}
	return (10 - sum%10) % 10
}
