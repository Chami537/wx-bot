package ilink

// SplitText splits text into chunks of at most maxRunes runes each, in order.
// A non-positive maxRunes is treated as MaxTextRunes.
func SplitText(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = MaxTextRunes
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return []string{text}
	}
	var chunks []string
	for len(runes) > 0 {
		end := maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}
