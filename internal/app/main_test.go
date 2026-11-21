package app

import (
	"github.com/Trlydev/SWG/internal/copies"
	"os"
	"testing"

	"github.com/Trlydev/SWG/internal/preserve"
	"github.com/Trlydev/SWG/internal/workshop"
)

// Tests never reach the real Open Workshop or pre-worm archive.
func TestMain(m *testing.M) {
	undoP := preserve.SetForTest(&preserve.Archive{Records: map[string]*preserve.Record{}})
	undoW := workshop.SetIndexForTest(&workshop.Index{})
	undoC := copies.SetForTest(&copies.Index{Items: map[string]copies.Entry{}})
	code := m.Run()
	undoC()
	undoP()
	undoW()
	os.Exit(code)
}
