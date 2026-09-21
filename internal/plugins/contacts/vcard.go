package contacts

import (
	"io"
	"mime/quotedprintable"
	"regexp"
	"strings"
)

// uidSafeChars restricts phone-provided contact UIDs (Android LOOKUP_KEYs)
// to filename-safe characters so they can't escape the cache directory.
var uidSafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// sanitizeUID strips everything but alphanumerics, dot, underscore and
// hyphen. Empty results are rejected by the caller.
func sanitizeUID(uid string) string {
	return uidSafeChars.ReplaceAllString(uid, "_")
}

// qpSoftBreakFold fuses a QP soft break (`=` EOL) with vCard folding (whitespace continuation).
var qpSoftBreakFold = regexp.MustCompile("=\n[ \t]")

// isQuotedPrintable reports whether field params request QP (ENCODING=QUOTED-PRINTABLE or QP).
func isQuotedPrintable(params string) bool {
	for _, tok := range strings.Split(params, ";") {
		kv := strings.SplitN(tok, "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) != "ENCODING" {
			continue
		}
		if v := strings.TrimSpace(kv[1]); v == "QUOTED-PRINTABLE" || v == "QP" {
			return true
		}
	}
	return false
}

// decodeQuotedPrintable RFC 2045-decodes s; false on invalid input (caller keeps raw). Assumes UTF-8.
func decodeQuotedPrintable(s string) (string, bool) {
	b, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(s)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// cleanDisplayValue strips control characters (terminal-escape injection
// via contact names is a classic) and truncates overlong fields.
func cleanDisplayValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxDisplayLen {
		s = s[:maxDisplayLen]
	}
	return s
}

// parseVCard extracts display fields from vCard 3.0 text with a stdlib
// line scan: unfold continuations, split Name:Value, drop parameters
// (TEL;TYPE=CELL:...). Returns the first FN and all TEL/EMAIL values.
func parseVCard(vcard string) (name string, phones, emails []string) {
	// Normalize newlines, fuse QP soft breaks with folding, unfold continuations.
	raw := strings.ReplaceAll(vcard, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = qpSoftBreakFold.ReplaceAllString(raw, "")
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		if (line[0] == ' ' || line[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	for _, line := range lines {
		sep := strings.IndexByte(line, ':')
		if sep < 0 {
			continue
		}
		field := strings.ToUpper(line[:sep])
		params := ""
		if i := strings.IndexByte(field, ';'); i >= 0 {
			params = field[i+1:]
			field = field[:i]
		}
		rawValue := line[sep+1:]
		if isQuotedPrintable(params) {
			if decoded, ok := decodeQuotedPrintable(rawValue); ok {
				rawValue = decoded
			}
		}
		value := cleanDisplayValue(rawValue)
		if value == "" {
			continue
		}
		switch field {
		case "FN":
			if name == "" {
				name = value
			}
		case "TEL":
			phones = append(phones, value)
		case "EMAIL":
			emails = append(emails, value)
		}
	}
	return name, phones, emails
}
