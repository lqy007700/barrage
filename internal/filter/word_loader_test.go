package filter

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadWordsFromFileIgnoresCommentsAndBlankLines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sensitive_words.txt")

	content := "# comment\n\n傻逼\n  垃圾  \n# another\n他妈的\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write word file: %v", err)
	}

	words, err := LoadWordsFromFile(path)
	if err != nil {
		t.Fatalf("load words: %v", err)
	}

	if len(words) != 3 {
		t.Fatalf("unexpected word count: got=%d words=%v", len(words), words)
	}

	if words[0] != "傻逼" || words[1] != "垃圾" || words[2] != "他妈的" {
		t.Fatalf("unexpected words: %v", words)
	}
}

func TestReloadableFilterReloadIfChanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sensitive_words.txt")

	if err := os.WriteFile(path, []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write initial word file: %v", err)
	}

	rf := NewReloadableFilter(path, []string{"fallback"})
	if err := rf.LoadNow(); err != nil {
		t.Fatalf("initial load: %v", err)
	}

	if !rf.Check("hello alpha").Hit {
		t.Fatalf("expected alpha to match after initial load")
	}

	time.Sleep(20 * time.Millisecond)

	if err := os.WriteFile(path, []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("write updated word file: %v", err)
	}

	if err := rf.reloadIfChanged(); err != nil {
		t.Fatalf("reload changed file: %v", err)
	}

	if rf.Check("hello alpha").Hit {
		t.Fatalf("expected alpha to stop matching after reload")
	}

	if !rf.Check("hello beta").Hit {
		t.Fatalf("expected beta to match after reload")
	}
}
