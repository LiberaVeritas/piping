package quota

import "errors"

var (
	ErrInsufficient = errors.New("insufficient quota")
	ErrGrantExists  = errors.New("grant already exists")
)

type Rates struct {
	ColorRate int
}

func (r Rates) Cost(pages, colorPages int) int {
	return (pages - colorPages) + colorPages*r.ColorRate
}
