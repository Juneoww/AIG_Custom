package utils

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type failingPrivateInput struct{}

func (failingPrivateInput) Read([]byte) (int, error) { return 0, errors.New("private-model-secret") }

func TestRunCmdPrivateInputFailureAndCancelAreSafe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture runs in Docker")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "reader")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\n"), 0700))
	for _, input := range []io.Reader{failingPrivateInput{}, bytes.NewReader([]byte("private-model-secret"))} {
		ctx, cancel := context.WithCancel(context.Background())
		if _, ok := input.(*bytes.Reader); ok {
			cancel()
		}
		defer cancel()
		err := RunCmdWithContextInput(ctx, dir, script, nil, input, nil, strings.NewReplacer("private-model-secret", "[REDACTED]").Replace, func(string) { t.Fatal("unexpected output") })
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private-model-secret")
	}
}

func TestRunCmdPrivateOutputRedactsAcrossWrites(t *testing.T) {
	var lines []string
	out := &privateCommandOutput{redact: strings.NewReplacer("private-model-secret", "[REDACTED]").Replace, callback: func(s string) { lines = append(lines, s) }}
	_, err := out.Write([]byte("private-model-"))
	require.NoError(t, err)
	_, err = out.Write([]byte("secret\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"[REDACTED]"}, lines)
}
