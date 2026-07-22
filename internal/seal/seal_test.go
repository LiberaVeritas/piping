package seal_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"pgregory.net/rapid"

	"piping/internal/seal"
)

type blob struct {
	A string `json:"a"`
	B int    `json:"b"`
}

func newTestSealer(t *testing.T) *seal.Sealer {
	t.Helper()
	s, err := seal.NewSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWrongKeyRejected(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		gen := rapid.SliceOfN(rapid.Byte(), 32, 32)
		a := gen.Draw(rt, "a")
		b := gen.Filter(func(x []byte) bool { return !bytes.Equal(x, a) }).Draw(rt, "b")

		s1, _ := seal.NewSealer(a)
		s2, _ := seal.NewSealer(b)
		sealed, _ := s1.SealAsJSON("test", blob{A: "x"})
		var out blob

		if err := s2.OpenAsJSON("test", sealed, &out); err == nil {
			rt.Fatal("blob sealed with another key opened")
		}
	})
}

func TestBadKeyLength(t *testing.T) {

	if _, err := seal.NewSealer([]byte("short")); err == nil {
		t.Fatal("keys must be 32 bytes")
	}
}

func TestRoundTrip(t *testing.T) {
	s := newTestSealer(t)
	rapid.Check(t, func(rt *rapid.T) {
		label := rapid.StringMatching(`[ -~]{0,32}`).Draw(rt, "label")
		in := blob{
			A: rapid.String().Draw(rt, "a"),
			B: rapid.IntRange(-1<<30, 1<<30).Draw(rt, "b"),
		}
		sealed, err := s.SealAsJSON(label, in)
		if err != nil {
			rt.Fatal(err)
		}
		var out blob
		err = s.OpenAsJSON(label, sealed, &out)
		if err != nil {
			rt.Fatal(err)
		}
		if out != in {
			rt.Fatalf("round trip: %+v -> %+v", in, out)
		}
	})
}

func TestAnyTamperRejected(t *testing.T) {
	s := newTestSealer(t)
	rapid.Check(t, func(rt *rapid.T) {
		in := blob{A: rapid.String().Draw(rt, "a")}
		sealed, err := s.SealAsJSON("test", in)
		if err != nil {
			rt.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(sealed)
		if err != nil {
			rt.Fatal(err)
		}
		i := rapid.IntRange(0, len(raw)-1).Draw(rt, "pos")
		raw[i] ^= 1
		tampered := base64.RawURLEncoding.EncodeToString(raw)
		var out blob
		err = s.OpenAsJSON("test", tampered, &out)
		if err == nil {
			rt.Fatalf("sealed blob tampered at %d opened", i)
		}
	})
}

func TestLabelSeparation(t *testing.T) {
	s := newTestSealer(t)
	rapid.Check(t, func(rt *rapid.T) {
		l1 := rapid.StringMatching(`[a-z_]{1,16}`).Draw(rt, "l1")
		l2 := rapid.StringMatching(`[a-z_]{1,16}`).
			Filter(func(x string) bool { return x != l1 }).Draw(rt, "l2")
		sealed, err := s.SealAsJSON(l1, blob{A: "x"})
		if err != nil {
			rt.Fatal(err)
		}
		var out blob
		err = s.OpenAsJSON(l2, sealed, &out)
		if err == nil {
			rt.Fatalf("blob sealed under %q opened under %q", l1, l2)
		}
	})
}
