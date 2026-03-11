package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/certs"
)

func TestStaticTraefikConfig_withCerts(t *testing.T) {
	pair := &certs.CertPair{
		CertFile: "/tmp/devedge.pem",
		KeyFile:  "/tmp/devedge-key.pem",
	}

	got := StaticTraefikConfig("/etc/traefik", "/etc/traefik/dynamic", pair)

	checks := []string{
		":80",
		":443",
		"websecure",
		"/etc/traefik/dynamic",
		"watch: true",
		"dashboard: true",
		"/tmp/devedge.pem",
		"/tmp/devedge-key.pem",
	}
	for _, c := range checks {
		if !strings.Contains(got, c) {
			t.Errorf("missing %q in:\n%s", c, got)
		}
	}
}

func TestStaticTraefikConfig_noCerts(t *testing.T) {
	got := StaticTraefikConfig("/etc/traefik", "/etc/traefik/dynamic", nil)
	if strings.Contains(got, "tls:") {
		t.Error("should not contain TLS section without cert pair")
	}
}

func TestWriteStaticConfig(t *testing.T) {
	dir := t.TempDir()
	pair := &certs.CertPair{CertFile: "/a.pem", KeyFile: "/a-key.pem"}

	if err := WriteStaticConfig(dir, dir+"/dynamic", pair); err != nil {
		t.Fatalf("WriteStaticConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "traefik.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), ":443") {
		t.Error("missing port 443")
	}
}
