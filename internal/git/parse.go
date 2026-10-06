package git

import (
	"bytes"
	"fmt"
	"github.com/markkaghazgarian/gitperch/internal/repository"
	"strconv"
	"strings"
)

// ParseStatus parses only fixed metadata fields; filenames remain opaque bytes.
func ParseStatus(data []byte) (repository.Status, error) {
	var s repository.Status
	bad := func(record string) (repository.Status, error) {
		return s, fmt.Errorf("malformed porcelain-v2 record: %s", SafeText(record))
	}
	if len(data) == 0 || data[len(data)-1] != 0 {
		return bad("missing NUL terminator")
	}
	records := bytes.Split(data[:len(data)-1], []byte{0})
	head, oid := false, false
	seen := map[string]bool{}
	for i := 0; i < len(records); i++ {
		x := string(records[i])
		if x == "" {
			return bad("empty record")
		}
		if strings.HasPrefix(x, "# ") {
			key, value, ok := strings.Cut(x[2:], " ")
			switch key {
			case "branch.oid", "branch.head", "branch.upstream", "branch.ab":
				if !ok || value == "" || seen[key] {
					return bad(x)
				}
				seen[key] = true
			default:
				continue
			}
			switch key {
			case "branch.oid":
				oid = true
				if value == "(initial)" {
					s.Unborn = true
				} else if objectID(value) {
					s.HeadOID = value
				} else {
					return bad(x)
				}
			case "branch.head":
				head = true
				s.Detached = value == "(detached)"
				if !s.Detached {
					s.Branch = value
				}
			case "branch.upstream":
				s.Upstream = value
			case "branch.ab":
				parts := strings.Split(value, " ")
				if len(parts) != 2 || !strings.HasPrefix(parts[0], "+") || !strings.HasPrefix(parts[1], "-") {
					return bad(x)
				}
				a, e1 := strconv.Atoi(parts[0][1:])
				b, e2 := strconv.Atoi(parts[1][1:])
				if e1 != nil || e2 != nil || a < 0 || b < 0 {
					return bad(x)
				}
				s.Ahead, s.Behind, s.ComparisonKnown = a, b, true
			}
			continue
		}
		if strings.HasPrefix(x, "? ") {
			if len(x) == 2 {
				return bad(x)
			}
			s.Untracked++
			continue
		}
		if strings.HasPrefix(x, "! ") {
			if len(x) == 2 {
				return bad(x)
			}
			continue
		}
		fields := 0
		modes := 0
		hashes := 0
		switch x[0] {
		case '1':
			fields, modes, hashes = 9, 3, 2
		case '2':
			fields, modes, hashes = 10, 3, 2
		case 'u':
			fields, modes, hashes = 11, 4, 3
		default:
			return bad(x)
		}
		p := strings.SplitN(x, " ", fields)
		if len(p) != fields || p[0] != x[:1] || p[fields-1] == "" || len(p[1]) != 2 || !validXY(p[1]) || !validSub(p[2]) {
			return bad(x)
		}
		for _, v := range p[3 : 3+modes] {
			if len(v) != 6 {
				return bad(x)
			}
			for _, r := range v {
				if r < '0' || r > '7' {
					return bad(x)
				}
			}
		}
		for _, v := range p[3+modes : 3+modes+hashes] {
			if !objectID(v) {
				return bad(x)
			}
		}
		if x[0] == '2' {
			score := p[8]
			if len(score) < 2 || (score[0] != 'R' && score[0] != 'C') {
				return bad(x)
			}
			n, err := strconv.Atoi(score[1:])
			if err != nil || n < 0 || n > 100 {
				return bad(x)
			}
			i++
			if i >= len(records) || len(records[i]) == 0 {
				return bad("missing rename source")
			}
		}
		s.Changes++
		if x[0] == 'u' {
			s.Conflicts++
		}
	}
	if !head || !oid || (s.ComparisonKnown && (s.Upstream == "" || s.Unborn || s.Detached)) {
		return bad("inconsistent or missing branch headers")
	}
	return s, nil
}

func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func validXY(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune(".MTADRCU", r) {
			return false
		}
	}
	return true
}
func validSub(s string) bool {
	return s == "N..." || len(s) == 4 && s[0] == 'S' && strings.ContainsRune(".C", rune(s[1])) && strings.ContainsRune(".M", rune(s[2])) && strings.ContainsRune(".U", rune(s[3]))
}
