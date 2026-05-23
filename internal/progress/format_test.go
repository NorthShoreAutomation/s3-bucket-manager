package progress

import (
	"testing"
	"time"
)

func TestFormatRate(t *testing.T) {
	tests := []struct {
		name string
		bps  float64
		want string
	}{
		{"zero", 0, "—/s"},
		{"negative", -1, "—/s"},
		{"bytes", 512, "512 B/s"},
		{"kilobytes", 2048, "2.0 KB/s"},
		{"megabytes", 5 * (1 << 20), "5.0 MB/s"},
		{"gigabytes", 3 * (1 << 30), "3.0 GB/s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatRate(tc.bps); got != tc.want {
				t.Errorf("FormatRate(%v) = %q, want %q", tc.bps, got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"negative", -1 * time.Second, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 4*time.Minute + 12*time.Second, "4m12s"},
		{"hours", 1*time.Hour + 23*time.Minute, "1h23m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDuration(tc.d); got != tc.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestParseRateBytesPerSecRoundTrip(t *testing.T) {
	tests := []float64{512, 2048, 5 * (1 << 20), 3 * (1 << 30)}
	for _, bps := range tests {
		got := ParseRateBytesPerSec(FormatRate(bps))
		// Allow 5% slack since FormatRate truncates to 1 decimal.
		diff := got - bps
		if diff < 0 {
			diff = -diff
		}
		if diff/bps > 0.05 {
			t.Errorf("round-trip %v -> %q -> %v drift exceeds 5%%", bps, FormatRate(bps), got)
		}
	}
}

func TestParseRateBytesPerSecEmpty(t *testing.T) {
	if ParseRateBytesPerSec("") != 0 {
		t.Errorf("expected 0 for empty input")
	}
	if ParseRateBytesPerSec("—/s") != 0 {
		t.Errorf("expected 0 for em-dash placeholder")
	}
}
