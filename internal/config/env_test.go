package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.env")
	require.NoError(t, os.WriteFile(path, []byte(`
# comment
REMATCH_TEST_PLAIN=plain
export REMATCH_TEST_EXPORTED=exported
REMATCH_TEST_QUOTED="quoted value"
REMATCH_TEST_EQUALS=a=b
REMATCH_TEST_PRESET=from-file
`), 0o600))
	t.Setenv("REMATCH_TEST_PRESET", "from-env")
	for _, k := range []string{"REMATCH_TEST_PLAIN", "REMATCH_TEST_EXPORTED", "REMATCH_TEST_QUOTED", "REMATCH_TEST_EQUALS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	require.NoError(t, LoadEnvFile(path))
	assert.Equal(t, "plain", os.Getenv("REMATCH_TEST_PLAIN"))
	assert.Equal(t, "exported", os.Getenv("REMATCH_TEST_EXPORTED"))
	assert.Equal(t, "quoted value", os.Getenv("REMATCH_TEST_QUOTED"))
	assert.Equal(t, "a=b", os.Getenv("REMATCH_TEST_EQUALS"))
	assert.Equal(t, "from-env", os.Getenv("REMATCH_TEST_PRESET"), "existing env vars win")
}

func TestLoadEnvFileRejectsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.env")
	require.NoError(t, os.WriteFile(path, []byte("NOT_A_PAIR\n"), 0o600))
	require.ErrorContains(t, LoadEnvFile(path), "bad.env:1")
}

func TestBool(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1", "yes", "on", " true "} {
		assert.True(t, Bool(v), v)
	}
	for _, v := range []string{"", "false", "0", "no", "off", "nope"} {
		assert.False(t, Bool(v), v)
	}
}
