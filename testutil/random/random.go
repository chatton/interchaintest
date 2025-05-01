package random

import (
	"math/rand"
)

var chars = []byte("abcdefghijklmnopqrstuvwxyz")

// LowerCaseLetterString returns a lowercase letter string of given length.
func LowerCaseLetterString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
