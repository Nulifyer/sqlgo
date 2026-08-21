package db

import (
	"net"
	"strconv"
	"strings"
)

// JoinHostPort formats a Config host and numeric port for network addresses.
// Config hosts are normally unbracketed, but accepting surrounding brackets
// keeps programmatic callers from producing double-bracketed IPv6 addresses.
func JoinHostPort(host string, port int) string {
	host = trimHostBrackets(host)
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// BracketIPv6Host formats a host without adding a port. IPv4 addresses and
// hostnames remain unchanged.
func BracketIPv6Host(host string) string {
	host = trimHostBrackets(host)
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func trimHostBrackets(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return host
}
