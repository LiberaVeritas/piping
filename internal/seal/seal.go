package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
)

type Sealer struct {
	aead cipher.AEAD
}

func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("seal key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) SealAsJSON(label string, v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("seal %s: %w", label, err)
	}
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	sealed := s.aead.Seal(nonce, nonce, plain, []byte(label))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Sealer) OpenAsJSON(label, sealed string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return fmt.Errorf("open %s: not base64url: %w", label, err)
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return fmt.Errorf("open %s: too short", label)
	}
	plain, err := s.aead.Open(nil, raw[:ns], raw[ns:], []byte(label))
	if err != nil {
		return fmt.Errorf("open %s: authentication failed", label)
	}
	if err := json.Unmarshal(plain, v); err != nil {
		return fmt.Errorf("open %s: %w", label, err)
	}
	return nil
}
