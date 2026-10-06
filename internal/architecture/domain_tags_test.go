package architecture

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// domainStructTagRE matches a struct tag carrying a serialisation or
// persistence mapping (`json:"..."` / `db:"..."`) inside a backtick literal.
var domainStructTagRE = regexp.MustCompile("`[^`]*\\b(?:json|db):\"")

// domainStructTagAllowlist whitelists files under internal/domain that may
// carry json/db struct tags, keyed by path relative to internal/domain, with
// the reason. It is empty on purpose: wire and persistence shapes belong to
// adapters (adapter DTOs / row mappers), never to the domain model. Add an
// entry only with a reason a reviewer would accept.
var domainStructTagAllowlist = map[string]string{}

// domainStructTagViolations returns every json/db struct tag in one
// non-test domain source file. Comment-only lines are skipped, as is a
// match that sits after a trailing `//`. Pure over (path, display name) so
// the fixture test can feed it a bad file.
func domainStructTagViolations(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []string
	lineNo := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		loc := domainStructTagRE.FindStringIndex(line)
		if loc == nil {
			continue
		}
		if c := strings.Index(line, "//"); c >= 0 && c < loc[0] {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d: struct tag on a domain type (%q) — JSON/DB shape is an adapter concern; map to an adapter DTO instead (see internal/adapters/kafka/cloudevents/wire.go)", path, lineNo, strings.TrimSpace(line)))
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return out
}

// TestNoStructTagsInDomain enforces that the domain layer is serialisation-
// and persistence-agnostic: no `json:"` / `db:"` struct tags in non-test
// files under internal/domain/**. Domain events used to carry their wire
// shape as json tags (internal/domain/shared/events.go); it now lives in the
// cloudevents adapter's DTOs, pinned by byte-exact golden files.
func TestNoStructTagsInDomain(t *testing.T) {
	const root = "../domain"
	for _, path := range goFilesUnder(t, root, false) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("rel %s: %v", path, err)
		}
		if _, ok := domainStructTagAllowlist[filepath.ToSlash(rel)]; ok {
			continue
		}
		for _, v := range domainStructTagViolations(t, path) {
			t.Error(v)
		}
	}
}

// TestNoStructTagsInDomainSensorFailsOnBadFixtures proves the sensor can
// fail: tagged fixtures must be reported, while untagged structs, comment
// mentions and other tag keys must not.
func TestNoStructTagsInDomainSensorFailsOnBadFixtures(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"json tag", "package x\n\ntype E struct {\n\tName string `json:\"name\"`\n}\n", true},
		{"json tag with omitempty", "package x\n\ntype E struct {\n\tName string `json:\"name,omitempty\"`\n}\n", true},
		{"db tag", "package x\n\ntype E struct {\n\tID string `db:\"id\"`\n}\n", true},
		{"mixed tags", "package x\n\ntype E struct {\n\tID string `yaml:\"id\" json:\"id\"`\n}\n", true},
		{"untagged struct", "package x\n\ntype E struct {\n\tName string\n}\n", false},
		{"comment mentions a tag", "package x\n\n// Name is `json:\"name\"` on the wire in the adapter.\ntype E struct{ Name string }\n", false},
		{"trailing comment mentions a tag", "package x\n\ntype E struct {\n\tName string // was `json:\"name\"`\n}\n", false},
		{"unrelated tag key", "package x\n\ntype E struct {\n\tName string `validate:\"required\"`\n}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixture.go")
			if err := os.WriteFile(p, []byte(tc.src), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got := domainStructTagViolations(t, p)
			if tc.wantErr && len(got) == 0 {
				t.Fatal("sensor did not flag the bad fixture")
			}
			if !tc.wantErr && len(got) != 0 {
				t.Fatalf("compliant fixture reported violations: %v", got)
			}
		})
	}
}
