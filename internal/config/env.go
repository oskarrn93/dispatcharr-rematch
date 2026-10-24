// Package config loads dispatcharr-rematch.env-style KEY=VALUE files.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LoadEnvFile sets each KEY=VALUE line of path in the process environment,
// without overriding variables that are already set. Blank lines, # comments
// and an `export ` prefix are allowed; matching surrounding quotes are
// stripped.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

// Bool parses a feature-flag style value ("true", "1", "yes", "on", ...);
// anything unrecognized, including "", is false.
func Bool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "on":
		return true
	}
	b, _ := strconv.ParseBool(strings.TrimSpace(value))
	return b
}
