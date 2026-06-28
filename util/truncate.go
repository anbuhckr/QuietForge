package util

const (
	DefaultTruncationLimit = 50000
	OverflowMessage        = "\n\n[Output truncated...]"
)

func TruncateOutput(text string, limit ...int) string {
	max := DefaultTruncationLimit
	if len(limit) > 0 && limit[0] > 0 {
		max = limit[0]
	}
	if len(text) <= max {
		return text
	}
	half := max / 2
	return text[:half] + OverflowMessage + text[len(text)-half:]
}

func TruncateLines(lines []string, maxLines ...int) []string {
	max := 5000
	if len(maxLines) > 0 && maxLines[0] > 0 {
		max = maxLines[0]
	}
	if len(lines) <= max {
		return lines
	}
	half := max / 2
	result := make([]string, 0, half+1+half)
	result = append(result, lines[:half]...)
	result = append(result, OverflowMessage)
	result = append(result, lines[len(lines)-half:]...)
	return result
}

func TruncateToolOutput(result string, maxLength ...int) string {
	return TruncateOutput(result, maxLength...)
}
