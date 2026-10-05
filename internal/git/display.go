package git

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var urlCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s]*@`)
var secretQuery = regexp.MustCompile(`(?i)([?&](?:access_token|token|password|private_token)=)[^&\s]+`)

// SafeText escapes control characters and removes URL userinfo from displays.
func SafeText(s string) string {
	s = urlCredentials.ReplaceAllString(s, "${1}[redacted]@")
	s = secretQuery.ReplaceAllString(s, "${1}[redacted]")
	var out strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			q := strconv.QuoteRune(r)
			out.WriteString(q[1 : len(q)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
