package share

import (
	"testing"
	"time"
)

func TestParseTTLValid(t *testing.T) {
	cases := []struct {
		in    string
		d     time.Duration
		never bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"never", 0, true},
	}
	for _, c := range cases {
		d, never, err := ParseTTL(c.in)
		if err != nil {
			t.Errorf("ParseTTL(%q) error: %v", c.in, err)
			continue
		}
		if d != c.d || never != c.never {
			t.Errorf("ParseTTL(%q) = %v, %v; want %v, %v", c.in, d, never, c.d, c.never)
		}
	}
}

func TestParseTTLInvalid(t *testing.T) {
	for _, in := range []string{"", "0d", "-1d", "+1d", "7", "7w", "1.5h", "d", "never ", "99999999999999999d"} {
		if _, _, err := ParseTTL(in); err == nil {
			t.Errorf("ParseTTL(%q) expected error", in)
		}
	}
}
