package portscan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePortScanMode(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Mode
	}{
		{name: "empty uses fixed AI default", input: "", want: FixedAI},
		{name: "fixed AI", input: "fixed_ai", want: FixedAI},
		{name: "full TCP", input: "full_tcp", want: FullTCP},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Normalize(test.input)

			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestNormalizePortScanModeRejectsNonCanonicalValues(t *testing.T) {
	for _, value := range []string{"full", "FIXED_AI", "Full_TCP", " fixed_ai", "fixed_ai "} {
		t.Run(value, func(t *testing.T) {
			_, err := Normalize(value)

			assert.Error(t, err)
		})
	}
}

func TestPortSpec(t *testing.T) {
	assert.Equal(t, "11434,1337,7000-9000,18789", PortSpec(FixedAI))
	assert.Equal(t, "1-65535", PortSpec(FullTCP))
	assert.Empty(t, PortSpec(Mode("unknown")))
}
