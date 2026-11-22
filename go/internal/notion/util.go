package notion

import (
	"math"
	"unicode/utf8"
)

func validUTF8(s string) bool { return utf8.ValidString(s) }

// jsRound is JavaScript's Math.round (halves round up).
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }
