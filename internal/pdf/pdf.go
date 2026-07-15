package pdf

import (
	"bytes"
	"errors"
)

var (
	ErrNotPDF     = errors.New("file is not a PDF")
	ErrUnreadable = errors.New("PDF could not be parsed")
)

func IsPDF(doc []byte) bool {
	return bytes.HasPrefix(doc, []byte("%PDF-"))
}
