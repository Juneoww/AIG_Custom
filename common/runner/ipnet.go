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
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"unicode"
)

const (
	MaxTargetExpressions       = 65536
	maxTargetExpressions       = MaxTargetExpressions
	maxTargetExpressionLineLen = 1 << 20
)

// ErrTooManyTargets indicates that expansion would exceed the batch limit.
var ErrTooManyTargets = errors.New("target expansion exceeds 65536 targets")

type ipv4Interval struct {
	start uint32
	count uint64
}

const ipv4CoverageThreshold uint64 = 64

type ipv4Coverage struct {
	intervals      []ipv4Interval
	smallIntervals map[ipv4Interval]struct{}
}

func newIPv4Coverage() *ipv4Coverage {
	return &ipv4Coverage{smallIntervals: make(map[ipv4Interval]struct{})}
}

func (coverage *ipv4Coverage) add(interval ipv4Interval) {
	if interval.count < ipv4CoverageThreshold {
		coverage.smallIntervals[interval] = struct{}{}
		return
	}
	coverage.intervals = addCoveredIPv4Interval(coverage.intervals, interval)
}

// ParseTargets trims, expands, and deduplicates a batch of target expressions.
// IPv4 CIDRs, complete IPv4 ranges, and trailing IPv4 wildcards are expanded.
func ParseTargets(expressions []string) ([]string, error) {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	coveredIPv4 := newIPv4Coverage()
	rawExpressionCount := 0
	for _, raw := range expressions {
		target := strings.TrimSpace(raw)
		if target == "" {
			continue
		}
		rawExpressionCount++
		if rawExpressionCount > maxTargetExpressions {
			return nil, ErrTooManyTargets
		}
		isURL := isHTTPURL(target)
		if !isURL && strings.ContainsAny(target, "\\~") {
			return nil, fmt.Errorf("invalid target %q: ranges cannot contain backslash or tilde", target)
		}
		if !isURL && strings.ContainsFunc(target, unicode.IsSpace) {
			return nil, fmt.Errorf("invalid target %q", target)
		}
		var expanded []string
		var err error
		switch {
		case isURL:
			expanded = []string{target}
		case isCIDRExpression(target):
			interval, intervalErr := parseCIDRInterval(target)
			if intervalErr != nil {
				err = intervalErr
				break
			}
			result, err = appendUncoveredIPv4Targets(result, seen, coveredIPv4, interval)
		case isIPv4PortRangeExpression(target):
			err = fmt.Errorf("IPv4 port ranges are not supported: %q", target)
		case isIPv6RangeExpression(target):
			err = fmt.Errorf("IPv6 ranges are not supported: %q", target)
		case isRangeExpression(target):
			interval, intervalErr := parseRangeInterval(target)
			if intervalErr != nil {
				err = intervalErr
				break
			}
			result, err = appendUncoveredIPv4Targets(result, seen, coveredIPv4, interval)
		case strings.Contains(target, "*"):
			interval, intervalErr := parseWildcardInterval(target)
			if intervalErr != nil {
				err = intervalErr
				break
			}
			result, err = appendUncoveredIPv4Targets(result, seen, coveredIPv4, interval)
		default:
			if isBracketedIPv6Address(target) {
				return nil, fmt.Errorf("IPv6 targets are not supported: %q", target)
			}
			if addr, parseErr := netip.ParseAddr(target); parseErr == nil {
				if addr.Is6() {
					return nil, fmt.Errorf("IPv6 targets are not supported: %q", target)
				}
				if addr.Is4() {
					result, err = appendUncoveredIPv4Targets(result, seen, coveredIPv4, ipv4Interval{start: ipv4Uint32(addr), count: 1})
					break
				}
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

func appendUncoveredIPv4Targets(result []string, seen map[string]struct{}, coverage *ipv4Coverage, interval ipv4Interval) ([]string, error) {
	if interval.count < ipv4CoverageThreshold {
		if _, ok := coverage.smallIntervals[interval]; ok {
			return result, nil
		}
	}
	uncovered := []ipv4Interval{interval}
	if interval.count >= ipv4CoverageThreshold {
		uncovered = uncoveredIPv4Intervals(coverage.intervals, interval)
	}
	for _, part := range uncovered {
		for offset := uint64(0); offset < part.count; offset++ {
			value := netip.AddrFrom4([4]byte{
				byte((part.start + uint32(offset)) >> 24),
				byte((part.start + uint32(offset)) >> 16),
				byte((part.start + uint32(offset)) >> 8),
				byte(part.start + uint32(offset)),
			}).String()
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
	coverage.add(interval)
	return result, nil
}

func uncoveredIPv4Intervals(covered []ipv4Interval, interval ipv4Interval) []ipv4Interval {
	start := uint64(interval.start)
	end := start + interval.count - 1
	result := make([]ipv4Interval, 0, 1)
	for _, existing := range covered {
		existingStart := uint64(existing.start)
		existingEnd := existingStart + existing.count - 1
		if existingEnd < start {
			continue
		}
		if existingStart > end {
			break
		}
		if existingStart > start {
			result = append(result, ipv4Interval{start: uint32(start), count: existingStart - start})
		}
		if existingEnd >= end {
			return result
		}
		start = existingEnd + 1
	}
	if start <= end {
		result = append(result, ipv4Interval{start: uint32(start), count: end - start + 1})
	}
	return result
}

func addCoveredIPv4Interval(covered []ipv4Interval, interval ipv4Interval) []ipv4Interval {
	start := uint64(interval.start)
	end := start + interval.count - 1
	result := make([]ipv4Interval, 0, len(covered)+1)
	inserted := false
	for _, existing := range covered {
		existingStart := uint64(existing.start)
		existingEnd := existingStart + existing.count - 1
		if existingEnd+1 < start {
			result = append(result, existing)
			continue
		}
		if end+1 < existingStart {
			if !inserted {
				result = append(result, ipv4Interval{start: uint32(start), count: end - start + 1})
				inserted = true
			}
			result = append(result, existing)
			continue
		}
		if existingStart < start {
			start = existingStart
		}
		if existingEnd > end {
			end = existingEnd
		}
	}
	if !inserted {
		result = append(result, ipv4Interval{start: uint32(start), count: end - start + 1})
	}
	return result
}

// AppendTargetExpressionLines appends non-empty target-list lines while
// bounding raw expressions before they reach ParseTargets.
func AppendTargetExpressionLines(expressions []string, content string) ([]string, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024), maxTargetExpressionLineLen)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(expressions) >= maxTargetExpressions {
			return nil, ErrTooManyTargets
		}
		expressions = append(expressions, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("invalid target expression list: %w", err)
	}
	return expressions, nil
}

func isRangeExpression(target string) bool {
	if isHTTPURL(target) {
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
	return isCompleteIPv4(parts[0]) ||
		(strings.Contains(parts[0], ".") && isCompleteIPv4(parts[1])) ||
		(looksLikeIPv4(parts[0]) && looksLikeIPv4(parts[1]))
}

func isHTTPURL(target string) bool {
	lowerTarget := strings.ToLower(target)
	return strings.HasPrefix(lowerTarget, "http://") || strings.HasPrefix(lowerTarget, "https://")
}

func isIPv4PortRangeExpression(target string) bool {
	if isHTTPURL(target) || !strings.Contains(target, "-") {
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
	if isHTTPURL(target) || !strings.Contains(target, "-") {
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

func isBracketedIPv6Address(target string) bool {
	if !strings.HasPrefix(target, "[") {
		return false
	}
	closing := strings.Index(target, "]")
	if closing <= 1 {
		return false
	}
	address, err := netip.ParseAddr(target[1:closing])
	return err == nil && address.Is6()
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
	if isHTTPURL(target) {
		return false
	}
	parts := strings.SplitN(target, "/", 2)
	if addr, err := netip.ParseAddr(parts[0]); err == nil {
		return addr.Is4() || addr.Is6()
	}
	return false
}

func expandCIDR(target string) ([]string, error) {
	interval, err := parseCIDRInterval(target)
	if err != nil {
		return nil, err
	}
	return expandIPv4Numbers(interval.start, interval.count), nil
}

func parseCIDRInterval(target string) (ipv4Interval, error) {
	prefix, err := netip.ParsePrefix(target)
	if err != nil {
		return ipv4Interval{}, fmt.Errorf("invalid CIDR %q: %w", target, err)
	}
	if !prefix.Addr().Is4() {
		return ipv4Interval{}, fmt.Errorf("IPv6 CIDR is not supported: %q", target)
	}
	prefix = prefix.Masked()
	count := uint64(1) << uint(32-prefix.Bits())
	if count > maxTargetExpressions {
		return ipv4Interval{}, ErrTooManyTargets
	}
	return ipv4Interval{start: ipv4Uint32(prefix.Addr()), count: count}, nil
}

func expandRange(target string) ([]string, error) {
	interval, err := parseRangeInterval(target)
	if err != nil {
		return nil, err
	}
	return expandIPv4Numbers(interval.start, interval.count), nil
}

func parseRangeInterval(target string) (ipv4Interval, error) {
	parts := strings.Split(target, "-")
	if len(parts) != 2 {
		return ipv4Interval{}, fmt.Errorf("invalid IPv4 range %q", target)
	}
	start, err := netip.ParseAddr(parts[0])
	if err != nil || !start.Is4() {
		return ipv4Interval{}, fmt.Errorf("invalid IPv4 range %q", target)
	}
	finish, err := netip.ParseAddr(parts[1])
	if err != nil || !finish.Is4() {
		return ipv4Interval{}, fmt.Errorf("invalid IPv4 range %q", target)
	}
	first := ipv4Uint32(start)
	last := ipv4Uint32(finish)
	if first > last {
		return ipv4Interval{}, fmt.Errorf("reversed IPv4 range %q", target)
	}
	count := uint64(last-first) + 1
	if count > maxTargetExpressions {
		return ipv4Interval{}, ErrTooManyTargets
	}
	return ipv4Interval{start: first, count: count}, nil
}

func expandWildcard(target string) ([]string, error) {
	interval, err := parseWildcardInterval(target)
	if err != nil {
		return nil, err
	}
	return expandIPv4Numbers(interval.start, interval.count), nil
}

func parseWildcardInterval(target string) (ipv4Interval, error) {
	parts := strings.Split(target, ".")
	if len(parts) != 4 {
		return ipv4Interval{}, fmt.Errorf("invalid IPv4 wildcard %q", target)
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
			return ipv4Interval{}, fmt.Errorf("invalid IPv4 wildcard %q", target)
		}
		value, err := netip.ParseAddr("0.0.0." + part)
		if err != nil || !value.Is4() {
			return ipv4Interval{}, fmt.Errorf("invalid IPv4 wildcard %q", target)
		}
	}
	if firstStar == -1 {
		return ipv4Interval{}, fmt.Errorf("invalid IPv4 wildcard %q", target)
	}
	count := uint64(1) << uint(8*(4-firstStar))
	if count > maxTargetExpressions {
		return ipv4Interval{}, ErrTooManyTargets
	}
	base := uint32(0)
	for i := 0; i < firstStar; i++ {
		value, _ := netip.ParseAddr("0.0.0." + parts[i])
		base = (base << 8) | uint32(value.As4()[3])
	}
	base <<= uint(8 * (4 - firstStar))
	return ipv4Interval{start: base, count: count}, nil
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
