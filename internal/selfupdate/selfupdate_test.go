package selfupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.12", "v0.1.9", true},
		{"v0.2.0", "v0.1.99", true},
		{"v0.1.9", "v0.1.9", false},
		{"v0.1.8", "v0.1.9", false},
		{"v1.0.0", "dev", false},
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Self-update only trusts SHA256SUMS.txt with the release key's signature.
func TestVerifySums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	old := publicKey
	publicKey = pub
	defer func() { publicKey = old }()
	sums := []byte("abc  ppgmods-linux-amd64\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	if err := verifySums(sums, sig+"\n"); err != nil {
		t.Errorf("valid signature refused: %v", err)
	}
	if err := verifySums(append(sums, 'x'), sig); err == nil {
		t.Error("changed checksums accepted")
	}
	_, other, _ := ed25519.GenerateKey(nil)
	if err := verifySums(sums, base64.StdEncoding.EncodeToString(ed25519.Sign(other, sums))); err == nil {
		t.Error("someone else's signature accepted")
	}
}
