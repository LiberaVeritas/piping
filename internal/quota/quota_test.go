package quota

import "testing"

func TestCost(t *testing.T) {
	cases := []struct {
		name              string
		rate              int
		pages, colorPages int
		want              int
	}{
		{"all mono", 5, 10, 0, 10},
		{"all colour", 5, 10, 10, 50},
		{"mixed", 5, 10, 4, 6 + 20},
		{"rate of one prices colour as mono", 1, 10, 4, 10},
		{"single mono page", 5, 1, 0, 1},
		{"single colour page", 5, 1, 1, 5},
		{"no pages", 5, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Rates{ColorRate: tc.rate}.Cost(tc.pages, tc.colorPages)
			if got != tc.want {
				t.Errorf("Cost(%d, %d) at rate %d = %d, want %d",
					tc.pages, tc.colorPages, tc.rate, got, tc.want)
			}
		})
	}
}

// a page is never free and colour is never cheaper than mono, whatever the
// mix: these are the two things a billing rate must not get wrong.
func TestCostIsAtLeastPageCountAndRisesWithColour(t *testing.T) {
	for rate := 1; rate <= 8; rate++ {
		r := Rates{ColorRate: rate}
		for pages := 1; pages <= 20; pages++ {
			prev := 0
			for colorPages := 0; colorPages <= pages; colorPages++ {
				got := r.Cost(pages, colorPages)
				if got < pages {
					t.Fatalf("Cost(%d, %d) at rate %d = %d, cheaper than %d mono pages",
						pages, colorPages, rate, got, pages)
				}
				if colorPages > 0 && got < prev {
					t.Fatalf("Cost(%d, %d) at rate %d = %d, less than with one fewer colour page (%d)",
						pages, colorPages, rate, got, prev)
				}
				prev = got
			}
		}
	}
}
