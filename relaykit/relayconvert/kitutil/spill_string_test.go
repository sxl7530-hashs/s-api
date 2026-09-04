package kitutil

import (
	"os"
	"strings"
	"testing"
)

func TestSpillStringWriterKeepsShortValuesInMemory(t *testing.T) {
	w := NewSpillStringWriter()
	t.Cleanup(func() { _ = w.Close() })
	_, err := w.WriteString("hello")
	if err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if w.file != nil {
		t.Fatal("short value unexpectedly spilled")
	}
}

func TestSpillStringWriterPreservesLargeValuesAndCleansFile(t *testing.T) {
	w := NewSpillStringWriter()
	value := strings.Repeat("x", spillStringMemoryLimit+1024)
	if _, err := w.WriteString(value); err != nil {
		t.Fatal(err)
	}
	if w.file == nil {
		t.Fatal("large value did not spill")
	}
	name := w.file.Name()
	if got := w.String(); got != value {
		t.Fatalf("large value changed: got %d want %d bytes", len(got), len(value))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("spill file still exists: %v", err)
	}
}

func TestSpillStringWriterResetClearsSpilledValue(t *testing.T) {
	w := NewSpillStringWriter()
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.WriteString(strings.Repeat("z", spillStringMemoryLimit+1)); err != nil {
		t.Fatal(err)
	}
	w.Reset()
	if w.Len() != 0 || w.String() != "" {
		t.Fatal("reset did not clear value")
	}
}
