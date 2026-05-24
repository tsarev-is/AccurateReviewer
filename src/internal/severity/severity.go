// Package severity centralises the ordering of finding-severity labels.
// Every renderer (console, HTML, JSON), the post-comments filter, the
// master's dedupe, and the review command's blocking check used to carry
// their own copy of {"critical":4, …}. Three independent copies drift
// the moment someone adds a new tier — so the table lives here once.
package severity

// Rank returns the numeric rank of a severity label. Unknown labels
// map to 0 (same as "info"), which keeps a typo from accidentally
// promoting a finding above the blocking threshold.
func Rank(label string) int {
	switch label {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	case "info":
		return 0
	}
	return 0
}

// AtLeast reports whether `actual` is at or above `threshold`.
func AtLeast(actual, threshold string) bool {
	return Rank(actual) >= Rank(threshold)
}
