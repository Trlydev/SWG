package workshop

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	return b.Bytes()
}

func serve(t *testing.T, body []byte) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	return srv
}

func sub(url string, body []byte) *Submission {
	sum := sha256.Sum256(body)
	return &Submission{Name: "Test", Author: "Me", Kind: "mod", Version: "1.0", Download: url, SHA256: hex.EncodeToString(sum[:]), Maintainers: []string{"me"}}
}

func TestCheck(t *testing.T) {
	good := zipOf(t, map[string]string{"Test/mod.json": `{"Name":"Test","Author":"Me","ModVersion":"1.0"}`, "Test/a.cs": "class A {}"})
	srv := serve(t, good)
	if r := Check(srv.Client(), sub(srv.URL, good), t.TempDir()); len(r.Problems) > 0 || r.Kind != "mod" || r.ScanMax != "none" {
		t.Fatalf("good mod: %+v", r)
	}
	worm := zipOf(t, map[string]string{"W/mod.json": `{"Name":"Test","Author":"Me"}`,
		"W/a.cs": "using Steamworks; class A { void B() { SteamUGC.SubmitItemUpdate(default, \"\"); } }"})
	srv2 := serve(t, worm)
	r := Check(srv2.Client(), sub(srv2.URL, worm), t.TempDir())
	if len(r.Problems) == 0 || !strings.Contains(strings.Join(r.Problems, " "), "worm") {
		t.Fatalf("worm mod: %+v", r)
	}
	contr := zipOf(t, map[string]string{"Tank/Tank.jaap": "x"})
	srv3 := serve(t, contr)
	if r := Check(srv3.Client(), sub(srv3.URL, contr), t.TempDir()); !strings.Contains(strings.Join(r.Problems, " "), "kind") {
		t.Fatalf("contraption submitted as a mod: %+v", r)
	}
}

func TestValidate(t *testing.T) {
	s := &Submission{Name: "X", Author: "Y", Kind: "skin", Version: "1", Download: "http://x/y.zip", SHA256: "abc", Maintainers: []string{"bad name!"}}
	if p := s.Validate(); len(p) < 4 {
		t.Fatalf("problems: %v", p)
	}
	if ValidSlug("../etc") || ValidSlug("A") || !ValidSlug("quick-draw") {
		t.Fatal("slugs")
	}
}

// Keys made with openssl (PEM private key, base64 SPKI public key) sign
// and verify; the built-in key parses; only an index signed with it is
// accepted.
func TestSigningKeyFormats(t *testing.T) {
	if parsePublicKey(PublicKey) == nil {
		t.Errorf("built-in key doesn't parse: %s", PublicKey)
	}
	pub, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	spki, _ := x509.MarshalPKIXPublicKey(pub)
	pubB64 := base64.StdEncoding.EncodeToString(spki)
	index := []byte(`{"entries":[]}` + "\n")
	sig, err := Sign(index, privPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(index, sig, pubB64) {
		t.Error("PEM-signed index doesn't verify with the SPKI public key")
	}

	saved := PublicKey
	defer func() { PublicKey = saved }()
	PublicKey = pubB64
	if _, err := parse(index, sig); err != nil {
		t.Errorf("signed index refused: %v", err)
	}
	if _, err := parse(append(index, ' '), sig); err == nil {
		t.Error("a changed index was accepted")
	}
	_, other, _ := ed25519.GenerateKey(nil)
	otherSig, _ := Sign(index, base64.StdEncoding.EncodeToString(other))
	if _, err := parse(index, otherSig); err == nil {
		t.Error("an index signed with another key was accepted")
	}
}

// The signing secret is read however it was pasted: the PEM file, squashed
// onto one line or without its marker lines, base64 DER, the raw key or its
// seed. A public key is refused with a clear message.
func TestSigningKeyPasted(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	body := base64.StdEncoding.EncodeToString(der)
	pubDER, _ := x509.MarshalPKIXPublicKey(pub)
	pubB64 := base64.StdEncoding.EncodeToString(pubDER)
	index := []byte("{}\n")
	for name, v := range map[string]string{
		"pem":      pemText,
		"one line": strings.ReplaceAll(pemText, "\n", " "),
		"body":     body,
		"raw":      base64.StdEncoding.EncodeToString(priv),
		"seed":     base64.StdEncoding.EncodeToString(priv.Seed()),
		"spaces":   "  " + body + "\n",
	} {
		sig, err := Sign(index, v)
		if err != nil || !Verify(index, sig, pubB64) {
			t.Errorf("%s: %v", name, err)
		}
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	for _, v := range []string{pubPEM, pubB64} {
		if _, err := Sign(index, v); err == nil || !strings.Contains(err.Error(), "public key") {
			t.Errorf("public key accepted or unclear: %v", err)
		}
	}
	if _, err := Sign(index, "not a key"); err == nil {
		t.Error("garbage accepted")
	}
}

// The index on the data branch stays readable: the main key is the one
// every release trusts.
func TestMainKeyIsTheOriginal(t *testing.T) {
	if PublicKey != "GVSJqtKqZ1ifxuUiuo2tXzsKUVRjYyRQOsilg5NLc6s=" {
		t.Fatal("the main key must stay the one older copies trust")
	}
}
