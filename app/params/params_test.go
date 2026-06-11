package params

import "testing"

// TestNativeTokenIdentityLocked freezes the canonical native-token identity so a
// later edit cannot silently drift the accounting denom or its display metadata.
func TestNativeTokenIdentityLocked(t *testing.T) {
	cases := []struct{ got, want, name string }{
		{NativeBaseDenom, "utwlt", "NativeBaseDenom"},
		{NativeDisplayDenom, "twlt", "NativeDisplayDenom"},
		{NativeSymbol, "TWLT", "NativeSymbol"},
		{NativeName, "Twilight", "NativeName"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Fatalf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if NativeExponent != 6 {
		t.Fatalf("NativeExponent = %d, want 6", NativeExponent)
	}
}

// TestBaseDenomNotADisplayString is the display-leak guard: the canonical
// stateful-accounting denom (utwlt) must never equal any display metadata string
// (twlt / TWLT / Twilight). Protocol accounting uses NativeBaseDenom only.
func TestBaseDenomNotADisplayString(t *testing.T) {
	for _, display := range []string{NativeDisplayDenom, NativeSymbol, NativeName} {
		if NativeBaseDenom == display {
			t.Fatalf("NativeBaseDenom %q must not equal display metadata %q", NativeBaseDenom, display)
		}
	}
}
