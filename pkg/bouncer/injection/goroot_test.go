package injection

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func runtimeGOROOT() string {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func regexpMustCompile(t *testing.T, p string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	return re
}
