package dns

import (
	"os"
	"strings"
	"testing"
)

func TestBuildSection(t *testing.T) {
	got := buildSection([]string{"b.dev.test", "a.dev.test"})

	if !strings.Contains(got, beginMarker) {
		t.Error("missing begin marker")
	}
	if !strings.Contains(got, endMarker) {
		t.Error("missing end marker")
	}
	// Should be sorted.
	aIdx := strings.Index(got, "a.dev.test")
	bIdx := strings.Index(got, "b.dev.test")
	if aIdx > bIdx {
		t.Error("hostnames should be sorted")
	}
	// Should use the dedicated devedge loopback address.
	if !strings.Contains(got, "127.0.0.2") {
		t.Error("missing devedge loopback address (127.0.0.2)")
	}
}

func TestBuildSection_empty(t *testing.T) {
	got := buildSection(nil)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestReplaceSection_append(t *testing.T) {
	content := "127.0.0.1\tlocalhost\n"
	section := buildSection([]string{"x.dev.test"})
	got := replaceSection(content, section)

	if !strings.Contains(got, "localhost") {
		t.Error("original content should be preserved")
	}
	if !strings.Contains(got, "x.dev.test") {
		t.Error("new section should be appended")
	}
}

func TestReplaceSection_update(t *testing.T) {
	content := "127.0.0.1\tlocalhost\n" +
		beginMarker + "\n" +
		"127.0.0.1\told.dev.test\n" +
		endMarker + "\n"

	section := buildSection([]string{"new.dev.test"})
	got := replaceSection(content, section)

	if strings.Contains(got, "old.dev.test") {
		t.Error("old section should be replaced")
	}
	if !strings.Contains(got, "new.dev.test") {
		t.Error("new section should be present")
	}
	if !strings.Contains(got, "localhost") {
		t.Error("original content should be preserved")
	}
}

func TestReplaceSection_remove(t *testing.T) {
	content := "127.0.0.1\tlocalhost\n" +
		beginMarker + "\n" +
		"127.0.0.1\tx.dev.test\n" +
		endMarker + "\n"

	got := replaceSection(content, "")

	if strings.Contains(got, "x.dev.test") {
		t.Error("section should be removed")
	}
	if strings.Contains(got, beginMarker) {
		t.Error("begin marker should be removed")
	}
	if !strings.Contains(got, "localhost") {
		t.Error("original content should be preserved")
	}
}

func TestSyncHosts(t *testing.T) {
	tmp := t.TempDir() + "/hosts"
	os.WriteFile(tmp, []byte("127.0.0.1\tlocalhost\n"), 0644)

	err := SyncHosts(tmp, []string{"api.foo.dev.test", "web.foo.dev.test"})
	if err != nil {
		t.Fatalf("SyncHosts: %v", err)
	}

	data, _ := os.ReadFile(tmp)
	content := string(data)

	if !strings.Contains(content, "api.foo.dev.test") {
		t.Error("missing api hostname")
	}
	if !strings.Contains(content, "web.foo.dev.test") {
		t.Error("missing web hostname")
	}
	if !strings.Contains(content, "localhost") {
		t.Error("original content should be preserved")
	}

	// Update: remove web, keep api.
	err = SyncHosts(tmp, []string{"api.foo.dev.test"})
	if err != nil {
		t.Fatalf("SyncHosts update: %v", err)
	}

	data, _ = os.ReadFile(tmp)
	content = string(data)
	if strings.Contains(content, "web.foo.dev.test") {
		t.Error("web hostname should be removed after update")
	}
	if !strings.Contains(content, "api.foo.dev.test") {
		t.Error("api hostname should remain")
	}
}

func TestSyncHosts_idempotent(t *testing.T) {
	tmp := t.TempDir() + "/hosts"
	os.WriteFile(tmp, []byte("127.0.0.1\tlocalhost\n"), 0644)

	SyncHosts(tmp, []string{"x.dev.test"})
	data1, _ := os.ReadFile(tmp)

	SyncHosts(tmp, []string{"x.dev.test"})
	data2, _ := os.ReadFile(tmp)

	if string(data1) != string(data2) {
		t.Error("SyncHosts should be idempotent")
	}
}

func TestRemoveSection(t *testing.T) {
	tmp := t.TempDir() + "/hosts"
	initial := "127.0.0.1\tlocalhost\n"
	os.WriteFile(tmp, []byte(initial), 0644)

	SyncHosts(tmp, []string{"x.dev.test"})
	RemoveSection(tmp)

	data, _ := os.ReadFile(tmp)
	content := string(data)
	if strings.Contains(content, "x.dev.test") {
		t.Error("section should be removed")
	}
	if !strings.Contains(content, "localhost") {
		t.Error("original content should remain")
	}
}
