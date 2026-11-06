package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewindear/free-clean-my-macos/internal/engine"
	"github.com/thewindear/free-clean-my-macos/internal/owner"
	"github.com/thewindear/free-clean-my-macos/internal/rules"
)

func TestScanCancellationReturnsAPartialReport(t *testing.T) {
	home := newHome(t)
	addFiles(t, filepath.Join(home, "Library", "Caches", "Big"), 12000)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()

	report, err := engine.Scan(ctx, scanOptions(home, nil))
	cancel()
	if err != nil {
		t.Fatalf("cancelled Scan returned an error: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("the test never cancelled the scan")
	}
	if report.Schema != engine.SchemaVersion {
		t.Errorf("schema = %d, want %d", report.Schema, engine.SchemaVersion)
	}
}

func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{
		filepath.Join(home, "Library", "Caches"),
		filepath.Join(home, "tmp"),
		filepath.Join(home, "system"),
		filepath.Join(home, "Applications"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func addFiles(t *testing.T, dir string, count int) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		name := filepath.Join(dir, fmt.Sprintf("f%06d", i))
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func scanOptions(home string, running func(owner.Owner) bool) engine.Options {
	return engine.Options{
		Home:       home,
		TmpDir:     filepath.Join(home, "tmp") + "/",
		Rules:      rules.Default(),
		AppsDirs:   []string{filepath.Join(home, "Applications")},
		ConfigDir:  filepath.Join(home, ".config", "free-clean-my-macos"),
		StateDir:   filepath.Join(home, ".local", "state", "free-clean-my-macos"),
		SelfBinary: filepath.Join(home, "not-the-binary"),
		SystemRoot: filepath.Join(home, "system"),
		Running:    running,
	}
}
