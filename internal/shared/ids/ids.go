package ids

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
)

func New(prefix string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	id := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
	if prefix == "" {
		return id
	}
	return prefix + "_" + id
}
