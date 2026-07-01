package version

import (
	"fmt"
	"strconv"
	"strings"
)

var (
	Version         = "0.1.3-dev"
	Commit          = ""
	BuildDate       = ""
	UpdatePublicKey = ""
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
}

func Info() BuildInfo {
	return BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}

func String() string {
	parts := []string{Version}
	if strings.TrimSpace(Commit) != "" {
		parts = append(parts, "commit="+Commit)
	}
	if strings.TrimSpace(BuildDate) != "" {
		parts = append(parts, "built="+BuildDate)
	}
	return strings.Join(parts, " ")
}

func Print(name string) string {
	return fmt.Sprintf("%s %s", name, String())
}

func IsVersionCommand(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-version" || args[0] == "version")
}

func NeedsUpdate(currentVersion, desiredVersion string) bool {
	if strings.TrimSpace(desiredVersion) == "" {
		return false
	}
	if strings.TrimSpace(currentVersion) == "" {
		return true
	}
	return Compare(currentVersion, desiredVersion) < 0
}

func Compare(a, b string) int {
	aa := parts(a)
	bb := parts(b)
	for i := 0; i < len(aa) || i < len(bb); i++ {
		var av, bv int
		if i < len(aa) {
			av = aa[i]
		}
		if i < len(bb) {
			bv = bb[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func parts(value string) []int {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if idx := strings.IndexAny(value, "+-"); idx >= 0 {
		value = value[:idx]
	}
	raw := strings.Split(value, ".")
	out := make([]int, 0, len(raw))
	for _, part := range raw {
		n, _ := strconv.Atoi(part)
		out = append(out, n)
	}
	return out
}
