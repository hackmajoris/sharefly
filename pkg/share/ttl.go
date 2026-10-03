package share

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

func ParseTTL(s string) (d time.Duration, never bool, err error) {
	if s == "never" {
		return 0, true, nil
	}
	invalid := fmt.Errorf("invalid ttl %q: want Nd, Nh, Nm or never", s)
	if len(s) < 2 {
		return 0, false, invalid
	}
	var unit time.Duration
	switch s[len(s)-1] {
	case 'd':
		unit = 24 * time.Hour
	case 'h':
		unit = time.Hour
	case 'm':
		unit = time.Minute
	default:
		return 0, false, invalid
	}
	digits := s[:len(s)-1]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false, invalid
		}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/int64(unit) {
		return 0, false, invalid
	}
	return time.Duration(n) * unit, false, nil
}
