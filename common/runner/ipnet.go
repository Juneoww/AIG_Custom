// Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Requirement: Any integration or derivative work must explicitly attribute
// Tencent Zhuque Lab (https://github.com/Tencent/AI-Infra-Guard) in its
// documentation or user interface, as detailed in the NOTICE file.

// Package runner ipnet实现
package runner

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

const maxTargetExpressions = 65536

// ErrTooManyTargets indicates that expansion would exceed the batch limit.
var ErrTooManyTargets = errors.New("target expansion exceeds 65536 targets")

// ParseTargets trims, expands, and deduplicates a batch of target expressions.
// IPv4 CIDRs, complete IPv4 ranges, and trailing IPv4 wildcards are expanded.
func ParseTargets(expressions []string) ([]string, error) {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range expressions {
		target := strings.TrimSpace(raw)
		if target == "" {
			continue
		}
		if strings.ContainsAny(target, "\\~") {
			return nil, fmt.Errorf("invalid target %q: ranges cannot contain backslash or tilde", target)
		}
		var expanded []string
		var err error
		switch {
		case isCIDRExpression(target):
			expanded, err = expandCIDR(target)
		case isIPv4PortRangeExpression(target):
			err = fmt.Errorf("IPv4 port ranges are not supported: %q", target)
		case isIPv6RangeExpression(target):
			err = fmt.Errorf("IPv6 ranges are not supported: %q", target)
		case isRangeExpression(target):
			expanded, err = expandRange(target)
		case strings.Contains(target, "*"):
			expanded, err = expandWildcard(target)
		default:
			if strings.ContainsAny(target, " \t\r\n") {
				return nil, fmt.Errorf("invalid target %q", target)
			}
			if addr, parseErr := netip.ParseAddr(target); parseErr == nil && addr.Is6() {
				return nil, fmt.Errorf("IPv6 targets are not supported: %q", target)
			}
			expanded = []string{target}
		}
		if err != nil {
			return nil, err
		}
		for _, value := range expanded {
			if _, ok := seen[value]; ok {
				continue
			}
			if len(result) >= maxTargetExpressions {
				return nil, ErrTooManyTargets
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result, nil
}

func isRangeExpression(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	parts := strings.Split(target, "-")
	if !strings.Contains(target, "-") || !strings.Contains(target, ".") {
		return false
	}
	if len(parts) != 2 {
		for _, part := range parts {
			if isCompleteIPv4(part) {
				return true
			}
		}
		return false
	}
	return isCompleteIPv4(parts[0]) || isCompleteIPv4(parts[1]) ||
		(looksLikeIPv4(parts[0]) && looksLikeIPv4(parts[1]))
}

func isIPv4PortRangeExpression(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || !strings.Contains(target, "-") {
		return false
	}
	for _, part := range strings.Split(target, "-") {
		host, _, hasPort := strings.Cut(part, ":")
		if hasPort && isCompleteIPv4(host) {
			return true
		}
	}
	return false
}

func isIPv6RangeExpression(target string) bool {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || !strings.Contains(target, "-") {
		return false
	}
	for _, part := range strings.Split(target, "-") {
		if address, err := netip.ParseAddr(part); err == nil && address.Is6() {
			return true
		}
		if strings.HasPrefix(part, "[") {
			if closing := strings.Index(part, "]"); closing > 1 {
				if address, err := netip.ParseAddr(part[1:closing]); err == nil && address.Is6() {
					return true
				}
			}
		}
	}
	return false
}

func isCompleteIPv4(value string) bool {
	addr, err := netip.ParseAddr(value)
	return err == nil && addr.Is4()
}

func looksLikeIPv4(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && char != '.' {
			return false
		}
	}
	return true
}

func isCIDRExpression(target string) bool {
	if !strings.Contains(target, "/") {
		return false
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	parts := strings.SplitN(target, "/", 2)
	if addr, err := netip.ParseAddr(parts[0]); err == nil {
		return addr.Is4() || addr.Is6()
	}
	return false
}

func expandCIDR(target string) ([]string, error) {
	prefix, err := netip.ParsePrefix(target)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", target, err)
	}
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("IPv6 CIDR is not supported: %q", target)
	}
	prefix = prefix.Masked()
	count := uint64(1) << uint(32-prefix.Bits())
	if count > maxTargetExpressions {
		return nil, ErrTooManyTargets
	}
	start := ipv4Uint32(prefix.Addr())
	return expandIPv4Numbers(start, count), nil
}

func expandRange(target string) ([]string, error) {
	parts := strings.Split(target, "-")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid IPv4 range %q", target)
	}
	start, err := netip.ParseAddr(parts[0])
	if err != nil || !start.Is4() {
		return nil, fmt.Errorf("invalid IPv4 range %q", target)
	}
	finish, err := netip.ParseAddr(parts[1])
	if err != nil || !finish.Is4() {
		return nil, fmt.Errorf("invalid IPv4 range %q", target)
	}
	first := ipv4Uint32(start)
	last := ipv4Uint32(finish)
	if first > last {
		return nil, fmt.Errorf("reversed IPv4 range %q", target)
	}
	count := uint64(last-first) + 1
	if count > maxTargetExpressions {
		return nil, ErrTooManyTargets
	}
	return expandIPv4Numbers(first, count), nil
}

func expandWildcard(target string) ([]string, error) {
	parts := strings.Split(target, ".")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid IPv4 wildcard %q", target)
	}
	firstStar := -1
	for i, part := range parts {
		if part == "*" {
			if firstStar == -1 {
				firstStar = i
			}
			continue
		}
		if strings.Contains(part, "*") || (firstStar != -1) {
			return nil, fmt.Errorf("invalid IPv4 wildcard %q", target)
		}
		value, err := netip.ParseAddr("0.0.0." + part)
		if err != nil || !value.Is4() {
			return nil, fmt.Errorf("invalid IPv4 wildcard %q", target)
		}
	}
	if firstStar == -1 {
		return nil, fmt.Errorf("invalid IPv4 wildcard %q", target)
	}
	count := uint64(1) << uint(8*(4-firstStar))
	if count > maxTargetExpressions {
		return nil, ErrTooManyTargets
	}
	base := uint32(0)
	for i := 0; i < firstStar; i++ {
		value, _ := netip.ParseAddr("0.0.0." + parts[i])
		base = (base << 8) | uint32(value.As4()[3])
	}
	base <<= uint(8 * (4 - firstStar))
	return expandIPv4Numbers(base, count), nil
}

func ipv4Uint32(addr netip.Addr) uint32 {
	bytes := addr.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

func expandIPv4Numbers(start uint32, count uint64) []string {
	result := make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, start+uint32(i))
		result = append(result, ip.String())
	}
	return result
}

// Targets returns all the targets within a cidr range or the single target
func Targets(target string) chan string {
	results := make(chan string)
	go func() {
		defer close(results)

		// Legacy best-effort API: parsing errors produce an empty channel.
		parsed, err := ParseTargets([]string{target})
		if err != nil {
			return
		}
		for _, value := range parsed {
			results <- value
		}
	}()
	return results
}

// IPAddresses returns all the IP addresses in a CIDR
func IPAddresses(cidr string) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return []string{}, err
	}
	return IPAddressesIPnet(ipnet), nil
}

// IPAddressesIPnet returns all IP addresses in an IPNet.
func IPAddressesIPnet(ipnet *net.IPNet) (ips []string) {
	// convert IPNet struct mask and address to uint32
	mask := binary.BigEndian.Uint32(ipnet.Mask)
	start := binary.BigEndian.Uint32(ipnet.IP)

	// find the final address
	finish := (start & mask) | (mask ^ 0xffffffff)

	// loop through addresses as uint32
	for i := start; i <= finish; i++ {
		// convert back to net.IP
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, i)
		ips = append(ips, ip.String())
	}
	return ips
}
