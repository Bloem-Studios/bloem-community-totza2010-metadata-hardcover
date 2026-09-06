package metadata

import (
	"strings"
	"unicode"
)

func NormalizeISBN(value string) string {
	var normalized strings.Builder
	for _, r := range value {
		switch {
		case r == '-' || unicode.IsSpace(r):
			continue
		case r >= '0' && r <= '9':
			normalized.WriteRune(r)
		case r == 'x' || r == 'X':
			normalized.WriteRune('X')
		default:
			return ""
		}
	}

	result := normalized.String()
	if len(result) != 10 && len(result) != 13 {
		return ""
	}
	if strings.Contains(result[:len(result)-1], "X") || (len(result) == 13 && strings.HasSuffix(result, "X")) {
		return ""
	}
	if len(result) == 10 && !validISBN10(result) {
		return ""
	}
	if len(result) == 13 && !validISBN13(result) {
		return ""
	}
	return result
}

func validISBN10(value string) bool {
	sum := 0
	for i, r := range value {
		var digit int
		if r == 'X' {
			digit = 10
		} else {
			digit = int(r - '0')
		}
		sum += digit * (10 - i)
	}
	return sum%11 == 0
}

func validISBN13(value string) bool {
	sum := 0
	for i, r := range value {
		digit := int(r - '0')
		if i%2 == 1 {
			sum += digit * 3
			continue
		}
		sum += digit
	}
	return sum%10 == 0
}

// ISBN13FromISBN10 converts an ISBN-10 to its ISBN-13 form by prefixing the
// Bookland code and recomputing the check digit. It returns an empty string
// when the input is not a valid ISBN-10. The mapping is exact: every ISBN-10
// has exactly one ISBN-13, so the two forms of one edition must agree.
func ISBN13FromISBN10(value string) string {
	normalized := NormalizeISBN(value)
	if len(normalized) != 10 {
		return ""
	}

	body := "978" + normalized[:9]
	sum := 0
	for i, r := range body {
		digit := int(r - '0')
		if i%2 == 1 {
			sum += digit * 3
			continue
		}
		sum += digit
	}
	check := (10 - sum%10) % 10
	return body + string(rune('0'+check))
}

// ISBNsAgree reports whether an ISBN-13 and an ISBN-10 describe the same
// edition. Two ISBNs that do not agree mean the record they came from mixes up
// two different books. It returns true when either side is missing or
// unusable, so a partial record is not treated as a conflict.
func ISBNsAgree(isbn13, isbn10 string) bool {
	thirteen := NormalizeISBN(isbn13)
	derived := ISBN13FromISBN10(isbn10)
	if thirteen == "" || derived == "" {
		return true
	}
	return thirteen == derived
}
