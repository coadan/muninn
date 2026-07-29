package cli

import "strconv"

func formatCodexCount(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	raw := strconv.FormatInt(value, 10)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	if negative {
		return "-" + raw
	}
	return raw
}

func formatCodexCountNoun(value int64, noun string) string {
	if value != 1 {
		noun += "s"
	}
	return formatCodexCount(value) + " " + noun
}

func truncateCodexLabel(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	return value[:max-1] + "…"
}
