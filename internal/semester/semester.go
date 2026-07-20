package semester

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func Current(t time.Time) int {
	y := t.Year()
	switch m := t.Month(); {
	case m >= time.January && m <= time.April:
		return y*100 + 1
	case m >= time.May && m <= time.August:
		return y*100 + 5
	default:
		return y*100 + 9
	}
}

func Code(semester string) (int, error) {
	season, year, ok := strings.Cut(strings.TrimSpace(semester), " ")
	if !ok {
		return 0, fmt.Errorf("parsing semester %s", semester)
	}
	y, err := strconv.Atoi(year)
	if err != nil {
		return 0, fmt.Errorf("parsing year %s: %w", year, err)
	}
	var month int
	switch season {
	case "Winter":
		month = 1
	case "Summer":
		month = 5
	case "Fall":
		month = 9
	default:
		return 0, fmt.Errorf("unknown season %q", season)
	}
	return y*100 + month, nil
}

func Name(code int) string {
	year := code / 100
	switch code % 100 {
	case 1:
		return "Winter " + strconv.Itoa(year)
	case 5:
		return "Summer " + strconv.Itoa(year)
	case 9:
		return "Fall " + strconv.Itoa(year)
	default:
		return "Semester " + strconv.Itoa(code)
	}
}
