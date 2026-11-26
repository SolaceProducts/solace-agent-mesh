// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package egressguard

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

var (
	ErrBlocked      = errors.New("address rejected by the egress guard")
	ErrPrivateRange = errors.New("address is in a private range the egress guard allows only when asked")
)

type privateRangeError struct {
	address string
	why     string
}

func (e *privateRangeError) Error() string {
	return fmt.Sprintf("dial %s: %s: %s", e.address, ErrBlocked, e.why)
}

func (e *privateRangeError) Is(target error) bool {
	return target == ErrBlocked || target == ErrPrivateRange
}

func DialControl(allowPrivate bool) func(network, address string, _ syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("dial %s: %w", address, err)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("dial %s: %w (host did not resolve to an IP)", address, ErrBlocked)
		}
		blocked, why := BlockedAddress(ip, allowPrivate)
		if !blocked {
			return nil
		}
		if stillBlockedWhenPrivateIsAllowed, _ := BlockedAddress(ip, true); !stillBlockedWhenPrivateIsAllowed {
			return &privateRangeError{address: address, why: why}
		}
		return fmt.Errorf("dial %s: %w: %s", address, ErrBlocked, why)
	}
}

func BlockedAddress(ip net.IP, allowPrivate bool) (bool, string) {
	if ip == nil {
		return true, "nil ip"
	}
	if embedded, ok := embeddedIPv4(ip); ok {
		return BlockedAddress(embedded, allowPrivate)
	}
	if why := alwaysBlockedReason(ip); why != "" {
		return true, why
	}
	if why := privateRangeReason(ip); why != "" && !allowPrivate {
		return true, why
	}
	return false, ""
}

func alwaysBlockedReason(ip net.IP) string {
	switch {
	case ip.IsUnspecified():
		return "unspecified address (0.0.0.0 / ::)"
	case prefixContains(nat64LocalUsePrefix, ip):
		return "NAT64 local-use prefix (RFC8215)"
	case isCloudMetadataAddress(ip):
		return "cloud instance metadata endpoint"
	case ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast():
		return "link-local (covers AWS instance metadata 169.254.169.254)"
	case ip.IsMulticast():
		return "multicast / reserved"
	}
	if v4 := ip.To4(); v4 != nil {
		return alwaysBlockedIPv4Reason(v4)
	}
	return ""
}

func alwaysBlockedIPv4Reason(v4 net.IP) string {
	switch {
	case prefixContains(thisNetworkIPv4, v4):
		return `"this network" (0.0.0.0/8)`
	case v4.Equal(net.IPv4bcast):
		return "broadcast"
	case v4[0] >= 224:
		return "multicast / reserved"
	}
	return ""
}

func privateRangeReason(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "loopback"
	case ip.IsPrivate(), prefixContains(siteLocalIPv6, ip):
		return "private network (RFC1918 / RFC4193)"
	case prefixContains(sharedAddressSpaceIPv4, ip):
		return "shared address space (RFC6598 carrier-grade NAT)"
	}
	return ""
}

var (
	thisNetworkIPv4        = netip.MustParsePrefix("0.0.0.0/8")
	sharedAddressSpaceIPv4 = netip.MustParsePrefix("100.64.0.0/10")
	nat64Prefix            = netip.MustParsePrefix("64:ff9b::/96")
	sixToFourPrefix        = netip.MustParsePrefix("2002::/16")
	ipv4CompatiblePrefix   = netip.MustParsePrefix("::/96")
	siteLocalIPv6          = netip.MustParsePrefix("fec0::/10")
	siitPrefix             = netip.MustParsePrefix("::ffff:0:0:0/96")
	nat64LocalUsePrefix    = netip.MustParsePrefix("64:ff9b:1::/48")
)

func addrOf(ip net.IP) (netip.Addr, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	return addr.Unmap(), ok
}

func prefixContains(prefix netip.Prefix, ip net.IP) bool {
	addr, ok := addrOf(ip)
	return ok && prefix.Contains(addr)
}

func isCloudMetadataAddress(ip net.IP) bool {
	addr, ok := addrOf(ip)
	if !ok {
		return false
	}
	switch addr.String() {
	case "fd00:ec2::254", "fd00:ec2::23", "fd20:ce::254", "100.100.100.200", "168.63.129.16":
		return true
	}
	return false
}

func embeddedIPv4(ip net.IP) (net.IP, bool) {
	if ip.To4() != nil || ip.IsLoopback() {
		return nil, false
	}
	v6 := ip.To16()
	switch {
	case v6 == nil:
		return nil, false
	case prefixContains(nat64Prefix, v6), prefixContains(ipv4CompatiblePrefix, v6), prefixContains(siitPrefix, v6):
		return v6[12:16], true
	case prefixContains(sixToFourPrefix, v6):
		return v6[2:6], true
	default:
		return nil, false
	}
}
