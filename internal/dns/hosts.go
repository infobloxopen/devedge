// Package dns manages local name resolution for devedge hostnames.
//
// Primary strategy: managed /etc/hosts entries within fenced markers.
// Enhancement on macOS: /etc/resolver/ drop-in for wildcard *.dev.test support.
package dns

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/infobloxopen/devedge/pkg/types"
)

const (
	beginMarker = "# BEGIN devedge — do not edit this section"
	endMarker   = "# END devedge"
)

// SyncHosts updates the managed section of the hosts file with entries for
// the given hostnames, all pointing to loopback. Hostnames are sorted for
// deterministic output.
func SyncHosts(hostsPath string, hostnames []string) error {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("read hosts file: %w", err)
	}

	original := string(data)
	updated := replaceSection(original, buildSection(hostnames))

	// Avoid unnecessary writes.
	if updated == original {
		return nil
	}

	return atomicWriteFile(hostsPath, []byte(updated), 0644)
}

// RemoveSection removes the devedge-managed section from the hosts file.
func RemoveSection(hostsPath string) error {
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		return fmt.Errorf("read hosts file: %w", err)
	}

	original := string(data)
	updated := replaceSection(original, "")

	if updated == original {
		return nil
	}

	return atomicWriteFile(hostsPath, []byte(updated), 0644)
}

// buildSection generates the fenced hosts block.
func buildSection(hostnames []string) string {
	if len(hostnames) == 0 {
		return ""
	}

	sorted := make([]string, len(hostnames))
	copy(sorted, hostnames)
	sort.Strings(sorted)

	var b strings.Builder
	b.WriteString(beginMarker + "\n")
	for _, h := range sorted {
		fmt.Fprintf(&b, "%s\t%s\n", types.EdgeIP, h)
	}
	b.WriteString(endMarker + "\n")
	return b.String()
}

// replaceSection replaces the fenced section in content. If no section exists
// and replacement is non-empty, it is appended. If replacement is empty, the
// existing section is removed.
func replaceSection(content, replacement string) string {
	start := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)

	if start == -1 || end == -1 {
		// No existing section.
		if replacement == "" {
			return content
		}
		// Append with a blank line separator.
		trimmed := strings.TrimRight(content, "\n")
		return trimmed + "\n\n" + replacement
	}

	// Find the end of the end marker line.
	endLine := end + len(endMarker)
	if endLine < len(content) && content[endLine] == '\n' {
		endLine++
	}

	before := content[:start]
	after := content[endLine:]

	if replacement == "" {
		// Remove the section and any trailing blank line.
		result := strings.TrimRight(before, "\n") + "\n" + strings.TrimLeft(after, "\n")
		return result
	}

	return before + replacement + after
}

// atomicWriteFile writes data to a temp file then renames it into place.
// For /etc/hosts we must preserve the original inode on some systems, so
// we fall back to direct write if rename fails (cross-device).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".devedge.tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		// Fallback: write directly (temp dir may be on different mount).
		return os.WriteFile(path, data, perm)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Cross-device rename; fall back to direct write.
		os.Remove(tmp)
		return os.WriteFile(path, data, perm)
	}
	return nil
}
