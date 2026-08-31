package portscan

import "errors"

// Mode identifies one approved TCP port discovery profile.
type Mode string

const (
	FixedAI Mode = "fixed_ai"
	FullTCP Mode = "full_tcp"

	DefaultMode Mode = FixedAI

	FixedAIPortSpec = "11434,1337,7000-9000,18789"
	FullTCPPortSpec = "1-65535"
)

var ErrInvalidMode = errors.New("invalid port scan mode")

// Normalize returns the default only for an omitted value. All other values
// must exactly match an approved mode.
func Normalize(value string) (Mode, error) {
	switch Mode(value) {
	case "":
		return DefaultMode, nil
	case FixedAI, FullTCP:
		return Mode(value), nil
	default:
		return "", ErrInvalidMode
	}
}

// PortSpec returns the approved TCP port expression for mode. Unknown modes
// have no port specification.
func PortSpec(mode Mode) string {
	switch mode {
	case FixedAI:
		return FixedAIPortSpec
	case FullTCP:
		return FullTCPPortSpec
	default:
		return ""
	}
}
