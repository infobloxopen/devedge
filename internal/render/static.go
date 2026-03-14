package render

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/pkg/types"
)

// StaticTraefikConfig generates the static Traefik configuration that sets up
// entrypoints, file provider, and TLS. This is written once during install
// or startup, not on every reconciliation.
func StaticTraefikConfig(traefikDir string, dynamicDir string, certPair *certs.CertPair) string {
	cfg := fmt.Sprintf(`entryPoints:
  web:
    address: "%s:80"
    http:
      redirections:
        entryPoint:
          to: websecure
          scheme: https
  websecure:
    address: "%s:443"

providers:
  file:
    directory: "%s"
    watch: true

api:
  dashboard: true
  insecure: true
`, types.EdgeIP, types.EdgeIP, dynamicDir)

	if certPair != nil {
		cfg += fmt.Sprintf(`
tls:
  stores:
    default:
      defaultCertificate:
        certFile: "%s"
        keyFile: "%s"
`, certPair.CertFile, certPair.KeyFile)
	}

	return cfg
}

// WriteStaticConfig writes the static Traefik config to the given directory.
func WriteStaticConfig(traefikDir string, dynamicDir string, certPair *certs.CertPair) error {
	content := StaticTraefikConfig(traefikDir, dynamicDir, certPair)
	path := filepath.Join(traefikDir, "traefik.yaml")
	return os.WriteFile(path, []byte(content), 0644)
}
