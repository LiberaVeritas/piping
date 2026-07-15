package app

import (
	"time"
)

func CurrentSemester(t time.Time) int {
	y := t.Year()
	switch m := t.Month(); {
	case m >= time.January && m <= time.April:
		return y*100 + 1
	case m >= time.May && m <= time.August:
		return y*100 + 5
	default: // September–December
		return y*100 + 9
	}
}
