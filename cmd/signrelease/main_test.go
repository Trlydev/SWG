package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"
)

// The key is read however it was pasted into the secret.
func TestParseKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	file := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	oneLine := strings.Join(strings.Fields(file), " ")
	bare := base64.StdEncoding.EncodeToString(der)
	for name, v := range map[string]string{"file": file, "one line": oneLine, "base64 only": bare, "spaces around": "\n " + file + " \n"} {
		k, err := parseKey(v)
		if err != nil || !k.Equal(priv) {
			t.Errorf("%s: %v", name, err)
		}
	}
	spki, _ := x509.MarshalPKIXPublicKey(priv.Public())
	pub := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki}))
	if _, err := parseKey(pub); err == nil || !strings.Contains(err.Error(), "public key") {
		t.Errorf("public key: %v", err)
	}
	if _, err := parseKey(""); err == nil {
		t.Error("empty secret accepted")
	}
}
