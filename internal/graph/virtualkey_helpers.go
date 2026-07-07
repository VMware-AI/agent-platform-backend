package graph

// Helpers for the VirtualKey resolver family. These live OUTSIDE
// virtualkey.resolvers.go on purpose: gqlgen rewrites *.resolvers.go files
// and (per its convention) tends to drop any helper code that the current
// schema's resolver set doesn't reference, even though those helpers are
// still in use. Pulling them out keeps them safe across future
// `make generate` runs.

import (
	"regexp"
	"strconv"
	"time"
)

// durationRE matches forms like "30d", "12h", "2w", "90m".
var durationRE = regexp.MustCompile(`^(\d+)([dhwm])$`)

// redactKey returns a safe-to-display preview of an API key.
//
//	"sk-aBcDeFgHiJkLmNoPqRsTuVwXyZ" → "sk-aBcD...XyZ"  (head 6 + "..." + tail 4)
//	< 12 chars                     → full string verbatim.
//
// Inline copy of gateway.redactKey (package-private). Single source of
// truth is in internal/gateway/client.go; if that signature changes,
// mirror it here.
func redactKey(plain string) string {
	if len(plain) < 12 {
		return plain
	}
	return plain[:6] + "..." + plain[len(plain)-4:]
}

// parseDuration returns time.Duration for "Nd"/"Nh"/"Nw"/"Nm" forms.
// Returns (0, false) when the input does not match.
func parseDuration(s string) (time.Duration, bool) {
	m := durationRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "d":
		return time.Duration(n) * 24 * time.Hour, true
	case "h":
		return time.Duration(n) * time.Hour, true
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, true
	case "m":
		return time.Duration(n) * 30 * 24 * time.Hour, true
	}
	return 0, false
}

// vkDerefBool returns *p if non-nil, else def. (Renamed to avoid collision
// with the same-named helper in model_spec.go.)
func vkDerefBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// vkDerefStr returns *p if non-nil+non-empty, else def.
func vkDerefStr(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}

// vkDerefInt returns *p if non-nil, else def.
func vkDerefInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// vkDerefFloat64 returns *p if non-nil, else def.
func vkDerefFloat64(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}
