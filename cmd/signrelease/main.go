// Command signrelease signs a release's SHA256SUMS.txt with the release
// key (Ed25519, PKCS#8 PEM in the RELEASE_SIGNING_KEY environment variable,
// a GitHub secret) and writes the signature next to it as <file>.sig
// (base64). ppgmods' self-update checks it with the public key it carries
// (internal/selfupdate) before trusting any checksum.
//
//	RELEASE_SIGNING_KEY=... go run ./cmd/signrelease dist/SHA256SUMS.txt
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: signrelease <file>")
	}
	key, err := parseKey(os.Getenv("RELEASE_SIGNING_KEY"))
	if err != nil {
		fail("RELEASE_SIGNING_KEY: " + err.Error())
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err.Error())
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))
	if err := os.WriteFile(os.Args[1]+".sig", []byte(sig+"\n"), 0o644); err != nil {
		fail(err.Error())
	}
	fmt.Println("signed", os.Args[1])
}

// parseKey reads the key file's content however it was pasted into the
// secret: as the file, as one line (the line breaks lost), or only the
// base64 between the BEGIN and END lines. The key itself is never printed.
func parseKey(v string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(v) == "" {
		return nil, errors.New("not set: add the release key as a repository secret")
	}
	der := []byte(nil)
	if blk, _ := pem.Decode([]byte(v)); blk != nil {
		der = blk.Bytes
	} else {
		body := v
		for _, marker := range []string{"-----BEGIN PRIVATE KEY-----", "-----END PRIVATE KEY-----"} {
			body = strings.ReplaceAll(body, marker, "")
		}
		if strings.Contains(body, "PUBLIC KEY") {
			return nil, errors.New("this is the public key; the secret needs the private key file (ppgmods-release-key.pem)")
		}
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
		if err != nil {
			return nil, errors.New("not a key file's content: paste the whole of ppgmods-release-key.pem")
		}
		der = b
	}
	if _, err := x509.ParsePKIXPublicKey(der); err == nil {
		return nil, errors.New("this is the public key; the secret needs the private key file (ppgmods-release-key.pem)")
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, errors.New("not a private key: paste the whole of ppgmods-release-key.pem")
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key (make it with openssl genpkey -algorithm ed25519)")
	}
	return key, nil
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "signrelease:", msg)
	os.Exit(1)
}
