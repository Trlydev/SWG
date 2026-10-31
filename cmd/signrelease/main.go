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
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: signrelease <file>")
	}
	blk, _ := pem.Decode([]byte(os.Getenv("RELEASE_SIGNING_KEY")))
	if blk == nil {
		fail("RELEASE_SIGNING_KEY is not set or not a PEM key: add the release key as a repository secret")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		fail("RELEASE_SIGNING_KEY: " + err.Error())
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		fail("RELEASE_SIGNING_KEY is not an Ed25519 key")
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

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "signrelease:", msg)
	os.Exit(1)
}
