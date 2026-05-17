package security

import (
	"crypto/rand"
	"errors"
	"testing"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestNewTokenPanicsWhenRandomSourceFails(t *testing.T) {
	original := rand.Reader
	rand.Reader = errReader{}
	t.Cleanup(func() { rand.Reader = original })

	defer func() {
		if recover() == nil {
			t.Fatalf("NewToken did not fail closed when crypto/rand failed")
		}
	}()
	_ = NewToken()
}
