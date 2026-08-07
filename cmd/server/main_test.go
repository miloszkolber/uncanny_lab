package main

import "testing"

func TestListenNetworkMatchesLiteralAddressFamily(t *testing.T) {
	for host, expected := range map[string]string{
		"0.0.0.0":   "tcp4",
		"127.0.0.1": "tcp4",
		"::":        "tcp6",
		"::1":       "tcp6",
		"localhost": "tcp",
	} {
		if actual := listenNetwork(host); actual != expected {
			t.Errorf("listenNetwork(%q) = %q, want %q", host, actual, expected)
		}
	}
}
