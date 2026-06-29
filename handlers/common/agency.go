package common

import "strings"

// AgencyPrefix extracts the agency prefix from a full stop ID like "1_75403"
// → "1". Returns "none" when no prefix is present.
func AgencyPrefix(fullStopID string) string {
	if i := strings.Index(fullStopID, "_"); i > 0 {
		return fullStopID[:i]
	}
	return "none"
}
