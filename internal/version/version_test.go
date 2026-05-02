package version

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTagMatchesVERSIONFile(t *testing.T) {
	// Tag is sourced from //go:embed of VERSION colocated with the
	// package. Confirm the embedded value matches what's on disk so
	// a future contributor who edits the file but breaks the embed
	// directive sees a loud failure.
	data, err := os.ReadFile(filepath.Join(".", "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(data))
	if Tag != want {
		t.Errorf("Tag = %q, VERSION = %q", Tag, want)
	}
}

func TestTagNonEmpty(t *testing.T) {
	if Tag == "" {
		t.Error("Tag is empty; VERSION file may not have been embedded")
	}
}

func TestStringReturnsTag(t *testing.T) {
	if got := String(); got != Tag {
		t.Errorf("String() = %q, Tag = %q", got, Tag)
	}
}

func TestTagShape(t *testing.T) {
	// Same shape that scripts/check-version.sh accepts — keeps the
	// release CI gate and the source code aligned. A leading "v"
	// is accepted but not required.
	re := regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$`)
	if !re.MatchString(Tag) {
		t.Errorf("Tag %q does not match canonical MAJOR.MINOR.PATCH shape", Tag)
	}
}
