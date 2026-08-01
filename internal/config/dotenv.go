package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// loadDotEnv reads a .env file and applies its KEY=VALUE pairs to the process
// environment, without clobbering variables that are already set. This lets a
// checked-out .env provide defaults while real environment variables (e.g.
// from the shell, systemd, or a container runtime) still win.
//
// The parser is intentionally minimal - it handles the common .env shape:
//
//	KEY=value
//	KEY="quoted value"      # quotes stripped, inline comments kept out
//	export KEY=value        # optional `export` prefix
//	# full-line comment / blank lines ignored
//
// It does not expand $VAR references or handle multi-line values, which is
// enough for this app's handful of config keys and avoids pulling in a
// dependency. A missing file is not an error.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		// strip an optional leading `export `.
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		// unquote a fully double- or single-quoted value.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		// environment wins over .env: only set when not already present.
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// envFileLocations returns the candidate .env paths to look for, in order:
// an explicit override first, then the current directory, then the
// executable's directory (so the binary can be run from anywhere and still
// pick up a .env sitting next to it).
func envFileLocations(explicit string) []string {
	if explicit != "" {
		return []string{explicit}
	}
	paths := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, fmt.Sprintf("%s.env", exe))
	}
	return paths
}
