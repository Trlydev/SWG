package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// pinvoke.dll declares one native method: hollowing gives it an IL body,
// and the result is still a readable .NET DLL.
func TestHollow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "dll", "pinvoke.dll"))
	if err != nil {
		t.Fatal(err)
	}
	out, n, err := Hollow(b)
	if err != nil || n != 1 {
		t.Fatalf("changed %d, err %v", n, err)
	}
	if _, err := ReadCLR(out); err != nil {
		t.Fatalf("hollowed DLL unreadable: %v", err)
	}
	if out2, n2, err := Hollow(out); err != nil || n2 != 0 || len(out2) != len(out) {
		t.Errorf("hollowing twice changed %d (err %v)", n2, err)
	}
	clean, _ := os.ReadFile(filepath.Join("testdata", "dll", "clean.dll"))
	if _, n, err := Hollow(clean); err != nil || n != 0 {
		t.Errorf("clean DLL: changed %d, err %v", n, err)
	}
}
