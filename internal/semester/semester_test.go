package semester

import (
	"strconv"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestNameCodeInverse(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		season := rapid.SampledFrom([]string{"Winter", "Summer", "Fall"}).Draw(rt, "season")
		year := rapid.IntRange(1900, 2200).Draw(rt, "year")
		s := season + " " + strconv.Itoa(year)
		code, err := Code(s)
		if err != nil {
			rt.Fatalf("Code(%q): %v", s, err)
		}
		if got := Name(code); got != s {
			rt.Fatalf("Name(Code(%q)) = %q", s, got)
		}
	})
}

func TestCurrentMonotonic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.Int64Range(946684800, 4102444800).Draw(rt, "a") // 2000..2100
		b := rapid.Int64Range(946684800, 4102444800).Draw(rt, "b")
		if a > b {
			a, b = b, a
		}
		ca, cb := Current(time.Unix(a, 0)), Current(time.Unix(b, 0))
		if ca > cb {
			rt.Fatalf("Current went backwards: %d -> %d", ca, cb)
		}
	})
}
