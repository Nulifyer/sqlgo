package db

import "testing"

func TestJoinHostPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{name: "hostname", host: "db.example.com", port: 5432, want: "db.example.com:5432"},
		{name: "ipv4", host: "127.0.0.1", port: 3306, want: "127.0.0.1:3306"},
		{name: "ipv6", host: "2001:db8::1", port: 1433, want: "[2001:db8::1]:1433"},
		{name: "bracketed_ipv6", host: "[2001:db8::1]", port: 1433, want: "[2001:db8::1]:1433"},
		{name: "ipv6_zone", host: "fe80::1%eth0", port: 8080, want: "[fe80::1%eth0]:8080"},
		{name: "bracketed_ipv6_zone", host: "[fe80::1%eth0]", port: 8080, want: "[fe80::1%eth0]:8080"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := JoinHostPort(tc.host, tc.port); got != tc.want {
				t.Fatalf("JoinHostPort(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
			}
		})
	}
}

func TestBracketIPv6Host(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want string
	}{
		{host: "db.example.com", want: "db.example.com"},
		{host: "127.0.0.1", want: "127.0.0.1"},
		{host: "2001:db8::1", want: "[2001:db8::1]"},
		{host: "[2001:db8::1]", want: "[2001:db8::1]"},
		{host: "fe80::1%eth0", want: "[fe80::1%eth0]"},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := BracketIPv6Host(tc.host); got != tc.want {
				t.Fatalf("BracketIPv6Host(%q) = %q, want %q", tc.host, got, tc.want)
			}
		})
	}
}
