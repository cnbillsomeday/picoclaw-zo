package fish

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestImportCardsConcurrentAddCard locks the full-file rewrite against
// concurrent AddCard calls (regression: ImportCards used to bypass
// withCardsLock, dropping cards added while an import was in flight).
func TestImportCardsConcurrentAddCard(t *testing.T) {
	workspace := t.TempDir()
	memoryFile := filepath.Join(t.TempDir(), "MEMORY.md")
	var bullets []string
	for i := 0; i < 20; i++ {
		bullets = append(bullets, fmt.Sprintf("- imported fact number %02d from memory md", i))
	}
	if err := os.WriteFile(memoryFile, []byte(strings.Join(bullets, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}

	const adders = 4
	const addsPerGoroutine = 10
	var wg sync.WaitGroup
	for g := 0; g < adders; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < addsPerGoroutine; i++ {
				text := fmt.Sprintf("live fact %d %d from gateway", g, i)
				if _, _, err := AddCard(workspace, NewCard{Text: text}); err != nil {
					t.Errorf("AddCard: %v", err)
				}
			}
		}(g)
	}
	imported, skipped, err := ImportCards(workspace, memoryFile)
	if err != nil {
		t.Fatalf("ImportCards: %v", err)
	}
	if imported != 20 {
		t.Fatalf("imported = %d, want 20 (skipped %d)", imported, skipped)
	}
	wg.Wait()

	cards, err := LoadCards(workspace)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cards {
		if seen[c.ID] {
			t.Fatalf("duplicate card id %q", c.ID)
		}
		seen[c.ID] = true
	}
	want := adders*addsPerGoroutine + 20
	if len(cards) != want {
		t.Fatalf("cards = %d, want %d (lost writes?)", len(cards), want)
	}
}
