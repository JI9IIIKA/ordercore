package platform

import (
	"net/netip"
	"testing"
)

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.169.254", "::1", "::ffff:127.0.0.1", "fc00::1", "100.64.1.2", "0.0.0.0", "224.0.0.1"} {
		if PublicIP(netip.MustParseAddr(s)) {
			t.Errorf("allowed %s", s)
		}
	}
	if !PublicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
}
func TestURLValidation(t *testing.T) {
	for _, url := range []string{"file:///etc/passwd", "http://user:secret@example.com", "http://127.0.0.1", "http://example.com:22", "not-a-url"} {
		if ValidateURL(url) == nil {
			t.Errorf("allowed %s", url)
		}
	}
	if e := ValidateURL("https://example.com/path"); e != nil {
		t.Fatal(e)
	}
}
