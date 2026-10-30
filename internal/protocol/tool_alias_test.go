package protocol

import "testing"

// The gateway appends the official tool set and its system prompt names those
// tools, so a model answers "shell" although the client declared "Bash". The
// alias resolves onto the declared tool.
func TestOfficialAliasResolvesOntoDeclaredTool(t *testing.T) {
	renames := NewToolRenames()
	renames.Add("Bash", "clint_aaaaaaaaaaaa")
	renames.Add("Read", "clint_bbbbbbbbbbbb")

	for alias, want := range map[string]string{"shell": "Bash", "execute": "Bash", "read": "Read"} {
		got, changed := renames.Resolve(alias)
		if got != want || !changed {
			t.Fatalf("Resolve(%q) = %q,%v; want %q", alias, got, changed, want)
		}
	}
}

// An alias must not invent a tool the client never declared: the name passes
// through as written.
func TestOfficialAliasNeverInventsATool(t *testing.T) {
	renames := NewToolRenames()
	renames.Add("Edit", "clint_cccccccccccc")
	if got, changed := renames.Resolve("grep"); changed || got != "grep" {
		t.Fatalf("Resolve(grep) = %q,%v with no Grep declared", got, changed)
	}
}

// An exact wire name still wins over any alias.
func TestExactWireNameBeatsAlias(t *testing.T) {
	renames := NewToolRenames()
	renames.Add("shell", "clint_dddddddddddd")
	renames.Add("Bash", "clint_aaaaaaaaaaaa")
	if got, _ := renames.Resolve("clint_dddddddddddd"); got != "shell" {
		t.Fatalf("exact wire name resolved to %q", got)
	}
}
