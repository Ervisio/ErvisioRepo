package pluginapi

import (
	"strconv"
	"strings"
	"unicode"
)

// tomlString renders s as a TOML basic string. Control characters are
// refused by the callers, so Go quoting is valid TOML.
func tomlString(s string) string { return strconv.Quote(s) }

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// tableName returns the name of a "[name]" line and whether it is an array
// of tables "[[name]]". ok is false for other lines.
func tableName(line string) (name string, array, ok bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "[[") {
		end := strings.Index(t, "]]")
		if end < 0 {
			return "", false, false
		}
		return strings.TrimSpace(t[2:end]), true, true
	}
	if strings.HasPrefix(t, "[") {
		end := strings.Index(t, "]")
		if end < 0 {
			return "", false, false
		}
		return strings.TrimSpace(t[1:end]), false, true
	}
	return "", false, false
}

func isKeyLine(line, key string) bool {
	t := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(t, key)
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(rest), "=")
}

// setKey sets section.key to value (already TOML encoded) keeping the rest
// of the file, comments included. A trailing comment on the replaced line is
// dropped. The key is added at the end of the section, or in a new section.
func setKey(src []byte, section, key, value string) []byte {
	crlf := strings.Contains(string(src), "\r\n")
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	newLine := key + " = " + value

	start, end := -1, len(lines) // body of the section: lines[start:end]
	for i, l := range lines {
		name, arr, ok := tableName(l)
		if !ok {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if name == section && !arr {
			start = i + 1
		}
	}
	switch {
	case start < 0:
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+section+"]", newLine)
	default:
		replaced := false
		for i := start; i < end; i++ {
			if isKeyLine(lines[i], key) {
				lines[i] = newLine
				replaced = true
				break
			}
		}
		if !replaced {
			at := end
			for at > start && strings.TrimSpace(lines[at-1]) == "" {
				at--
			}
			lines = append(lines[:at], append([]string{newLine}, lines[at:]...)...)
		}
	}
	out := strings.Join(lines, "\n") + "\n"
	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return []byte(out)
}
