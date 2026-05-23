package progress

import (
	"fmt"
	"strings"
	"time"
)

// FormatRate formats bytes/s into a human-readable string like "12.3 MB/s".
// Zero and negative values render as "—/s".
func FormatRate(bps float64) string {
	if bps <= 0 {
		return "—/s"
	}
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case bps >= gb:
		return fmt.Sprintf("%.1f GB/s", bps/gb)
	case bps >= mb:
		return fmt.Sprintf("%.1f MB/s", bps/mb)
	case bps >= kb:
		return fmt.Sprintf("%.1f KB/s", bps/kb)
	default:
		return fmt.Sprintf("%.0f B/s", bps)
	}
}

// ParseRateBytesPerSec is the inverse of FormatRate, used so the ETA
// calculation can be driven by the displayed rate rather than a raw float.
// Returns 0 on unparseable input or the em-dash placeholder.
func ParseRateBytesPerSec(r string) float64 {
	if r == "—/s" || r == "" {
		return 0
	}
	var val float64
	var unit string
	if _, err := fmt.Sscanf(r, "%f %s", &val, &unit); err != nil {
		return 0
	}
	switch {
	case strings.HasPrefix(unit, "GB"):
		return val * (1 << 30)
	case strings.HasPrefix(unit, "MB"):
		return val * (1 << 20)
	case strings.HasPrefix(unit, "KB"):
		return val * (1 << 10)
	default:
		return val
	}
}

// FormatDuration renders a duration as "1h23m", "4m12s", or "45s".
// Negative durations render as "0s".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
