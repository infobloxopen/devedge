package dnsserver

import (
	"context"
	"testing"
)

func TestStaticSuffixSource_ReturnsDefensiveCopy(t *testing.T) {
	src := NewStaticSuffixSource("dev.test", "example.com")

	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d items, want 2", len(got))
	}

	got[0] = ConfiguredSuffix{Name: "mutated"}

	again, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, cs := range again {
		if cs.Name == "mutated" {
			t.Errorf("caller mutation leaked into source: %v", again)
		}
	}
}

func TestStaticSuffixSource_SetUpdatesNextList(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")

	src.Set([]string{"other.test"})

	got, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "other.test" {
		t.Errorf("List after Set = %v, want [other.test]", got)
	}
}

func TestStaticSuffixSource_RespectsContextCancellation(t *testing.T) {
	src := NewStaticSuffixSource("dev.test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := src.List(ctx); err == nil {
		t.Errorf("List with canceled context returned nil error")
	}
}

func TestStaticSuffixSource_Name(t *testing.T) {
	src := NewStaticSuffixSource()
	if src.Name() != "static" {
		t.Errorf("Name() = %q, want %q", src.Name(), "static")
	}
}
