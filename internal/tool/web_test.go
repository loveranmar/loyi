package tool

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"127.0.0.1":       false,
		"10.0.0.5":        false,
		"192.168.1.1":     false,
		"172.16.0.1":      false,
		"169.254.169.254": false, // cloud metadata
		"::1":             false,
		"fe80::1":         false,
	}
	for ip, want := range cases {
		if got := isPublicIP(net.ParseIP(ip)); got != want {
			t.Errorf("isPublicIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestGuardHostBlocksPrivate(t *testing.T) {
	// IP literals don't hit the network in LookupIP, so this is offline.
	if err := guardHost("127.0.0.1"); err == nil {
		t.Error("expected loopback to be blocked")
	}
	if err := guardHost("169.254.169.254"); err == nil {
		t.Error("expected metadata address to be blocked")
	}
	if err := guardHost("8.8.8.8"); err != nil {
		t.Errorf("expected public address to pass, got %v", err)
	}
}

func TestSafeGetRejectsBadScheme(t *testing.T) {
	if _, _, err := safeGet(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("expected file:// scheme to be rejected")
	}
	if _, _, err := safeGet(context.Background(), "ftp://example.com"); err == nil {
		t.Error("expected ftp:// scheme to be rejected")
	}
}

func TestSafeGetRejectsPrivateHost(t *testing.T) {
	// guard runs before any request, so no network is touched.
	if _, _, err := safeGet(context.Background(), "http://127.0.0.1:8080/admin"); err == nil {
		t.Error("expected fetch of loopback to be refused")
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<html><head><style>.x{color:red}</style><title>t</title></head>` +
		`<body><script>evil()</script><h1>Hi</h1><p>Line&nbsp;one.</p><p>Two</p></body></html>`
	out := htmlToText(in)
	if strings.Contains(out, "evil") || strings.Contains(out, "color:red") {
		t.Errorf("script/style leaked into text: %q", out)
	}
	if !strings.Contains(out, "Hi") || !strings.Contains(out, "Line one.") || !strings.Contains(out, "Two") {
		t.Errorf("expected visible text preserved, got %q", out)
	}
}

func TestDDGCleanURL(t *testing.T) {
	wrapped := "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage&rut=abc"
	if got := ddgCleanURL(wrapped); got != "https://example.com/page" {
		t.Errorf("ddgCleanURL unwrap = %q, want https://example.com/page", got)
	}
	plain := "https://example.org/x"
	if got := ddgCleanURL(plain); got != plain {
		t.Errorf("ddgCleanURL(plain) = %q, want unchanged", got)
	}
}
