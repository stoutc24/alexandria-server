package config

import (
	"reflect"
	"testing"
)

func prefixStrings(t *testing.T, raw string) []string {
	t.Helper()
	var out []string
	for _, p := range trustedProxies(raw) {
		out = append(out, p.String())
	}
	return out
}

func TestTrustedProxiesReadsAddressesAndRanges(t *testing.T) {
	got := prefixStrings(t, " 172.18.0.5, 172.16.0.0/12 ,::1,fd00::/8, 10.1.2.3/8, "+
		"::ffff:192.168.1.9, ::ffff:10.0.0.0/104, fc00::/7")
	want := []string{"172.18.0.5/32", "172.16.0.0/12", "::1/128", "fd00::/8", "10.0.0.0/8",
		"192.168.1.9/32", "10.0.0.0/8", "fc00::/7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A typo, or a range wider than the widest private one, is skipped:
// never a reason to refuse to boot, and never a reason to trust more.
func TestTrustedProxiesSkipsWhatItCannotUse(t *testing.T) {
	got := prefixStrings(t, "nginx, 300.1.1.1, 0.0.0.0/0, ::/0, 10.0.0.0/33, , "+
		"0.0.0.0/1, 128.0.0.0/1, 10.0.0.0/7, fc00::/6, ::ffff:0.0.0.0/95, 192.168.1.1")
	if want := []string{"192.168.1.1/32"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := trustedProxies(""); len(got) != 0 {
		t.Fatalf("unset gave %v, want nothing", got)
	}
}
