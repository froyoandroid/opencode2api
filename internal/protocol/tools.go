package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// ClientToolPrefix marks every client tool the gateway renames on a shaped
// free-tier request. The prefix keeps client tools distinct from the official
// OpenCode set the gateway appends, and lets responses be restored to the names
// the client declared. What follows it is a digest, never the client name: the
// free tier refuses bodies that still read as another client's session.
const ClientToolPrefix = "clint_"

// officialToolAliases maps an official OpenCode tool name onto the client tool
// names that mean the same thing, most specific first. The gateway appends the
// official tool set to a shaped request and injects the official system
// prompt, whose text names those tools, so a model answers "shell" or "grep"
// even though the client never declared them; the client then fails the turn
// with "No such tool available: shell". An alias resolves only onto a name the
// client actually declared, so nothing is invented for a client that has no
// equivalent.
var officialToolAliases = map[string][]string{
	"shell":     {"Bash", "Shell", "terminal", "exec", "Execute"},
	"execute":   {"Bash", "Execute", "Shell", "terminal"},
	"bash":      {"Bash", "Shell", "terminal"},
	"read":      {"Read", "read_file", "ReadFile"},
	"edit":      {"Edit", "apply_patch", "patch", "EditFile"},
	"patch":     {"apply_patch", "Edit", "patch", "str_replace_editor"},
	"write":     {"Write", "write_file", "WriteFile"},
	"glob":      {"Glob", "list_files", "Glob"},
	"grep":      {"Grep", "search_files", "Search"},
	"webfetch":  {"WebFetch", "fetch"},
	"websearch": {"WebSearch", "search_web"},
	"task":      {"Task", "Agent", "subagent"},
	"subagent":  {"Task", "Agent", "subagent"},
	"skill":     {"Skill"},
	"todowrite": {"TodoWrite", "todo_write"},
	"question":  {"AskUserQuestion", "ask_user"},
}

// clientToolDigestLength is how many hex characters of the client name digest
// a wire name carries. Twelve is far past the collision point for the handful
// of tools one request declares.
const clientToolDigestLength = 12

// toolWireNameLimit is the longest tool name the upstreams accept.
const toolWireNameLimit = 64

// ToolRenames records the client-to-wire tool names of one shaped request. The
// zero value and a nil pointer are both empty and safe to use.
type ToolRenames struct {
	toWire   map[string]string
	toClient map[string]string
	// byCase and byShape map a folded client name back to the one declared
	// client tool it names. A value of "" marks a fold two tools share: that
	// name is never guessed between.
	byCase  map[string]string
	byShape map[string]string
}

// NewToolRenames returns an empty rename set.
func NewToolRenames() *ToolRenames {
	return &ToolRenames{
		toWire:   map[string]string{},
		toClient: map[string]string{},
		byCase:   map[string]string{},
		byShape:  map[string]string{},
	}
}

// Empty reports whether no tool was renamed.
func (r *ToolRenames) Empty() bool {
	return r == nil || len(r.toWire) == 0
}

// Count reports how many client tools were renamed.
func (r *ToolRenames) Count() int {
	if r == nil {
		return 0
	}
	return len(r.toWire)
}

// Wire returns the wire name registered for an exact client name.
func (r *ToolRenames) Wire(client string) (string, bool) {
	if r == nil || client == "" {
		return "", false
	}
	wire, ok := r.toWire[client]
	return wire, ok
}

// Client returns the client name registered for an exact wire name.
func (r *ToolRenames) Client(wire string) (string, bool) {
	if r == nil || wire == "" {
		return "", false
	}
	client, ok := r.toClient[wire]
	return client, ok
}

// Add records one client-to-wire mapping. Adding the same pair twice is a
// no-op; a conflicting pair keeps the first wire name so the map stays
// invertible.
func (r *ToolRenames) Add(client, wire string) {
	if r == nil || client == "" || wire == "" || client == wire {
		return
	}
	if existing, ok := r.toWire[client]; ok {
		if existing != wire {
			return
		}
	}
	if _, taken := r.toClient[wire]; taken {
		return
	}
	r.toWire[client] = wire
	r.toClient[wire] = client
	r.bindFolded(r.byCase, strings.ToLower(client), client)
	r.bindFolded(r.byShape, foldToolName(client), client)
}

// bindFolded records client under key in a folded map. An empty key names
// nothing (a client tool spelled only with separators must not be handed every
// other such name), and a key two clients share is marked ambiguous.
func (r *ToolRenames) bindFolded(folded map[string]string, key, client string) {
	if folded == nil || key == "" {
		return
	}
	if existing, ok := folded[key]; ok {
		if existing != client {
			folded[key] = ""
		}
		return
	}
	folded[key] = client
}

// toolNameSeparators are the characters ignored when matching a tool name by
// shape, after case: "todo_write", "todo-write", "todo.write" and "todowrite"
// all fold to one key.
const toolNameSeparators = "_.- \t\r\n"

// foldToolName lowercases name and removes the separators agents spell tool
// names with, so two spellings of one tool fold together.
func foldToolName(name string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(name) {
		if !strings.ContainsRune(toolNameSeparators, char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

// HasPrefix reports whether any wire name starts with prefix.
func (r *ToolRenames) HasPrefix(prefix string) bool {
	if r == nil || prefix == "" {
		return false
	}
	for wire := range r.toClient {
		if strings.HasPrefix(wire, prefix) {
			return true
		}
	}
	return false
}

// HasLongerPrefix reports whether any wire name is longer than prefix and
// starts with it. A stream fragment builder uses this to hold a partial name
// back until the next chunk resolves it.
func (r *ToolRenames) HasLongerPrefix(prefix string) bool {
	if r == nil || prefix == "" {
		return false
	}
	for wire := range r.toClient {
		if len(wire) > len(prefix) && strings.HasPrefix(wire, prefix) {
			return true
		}
	}
	return false
}

// declaredAlias resolves a name the gateway itself put on the wire (an
// official OpenCode tool) onto a client tool the request actually declared.
// The client's exact spelling is preferred, then a case-insensitive match, and
// a lookup that finds nothing declared reports false so the name passes
// through untouched rather than being invented.
func (r *ToolRenames) declaredAlias(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	for _, candidate := range officialToolAliases[strings.ToLower(name)] {
		if _, declared := r.toWire[candidate]; declared {
			return candidate, true
		}
		for client := range r.toWire {
			if strings.EqualFold(client, candidate) {
				return client, true
			}
		}
	}
	return "", false
}

// Resolve maps a tool name the upstream reported back to the name the client
// declared. It is the response-side counterpart of the wire rename: an exact
// wire name restores, a clint_ prefix is stripped as a best-effort recovery
// (for example after a configuration reload mid-stream), and a name that
// differs from exactly one declared client tool only in case, or else only in
// case and separators (todowrite → todo_write), is folded onto that tool. A
// name two tools fold to is never guessed between. It reports whether the
// result differs from the name given.
func (r *ToolRenames) Resolve(wire string) (string, bool) {
	if r == nil || wire == "" {
		return wire, false
	}
	if client, ok := r.Client(wire); ok {
		return client, client != wire
	}
	client := wire
	if rest, ok := strings.CutPrefix(wire, ClientToolPrefix); ok && rest != "" {
		client = rest
	}
	if _, declared := r.toWire[client]; declared {
		return client, client != wire
	}
	// The official tool set the gateway appends is not in toWire: it is not a
	// rename, it is the gateway's own vocabulary reaching a client that never
	// declared it. Resolve it onto the declared equivalent if there is one.
	if declared, ok := r.declaredAlias(client); ok {
		return declared, declared != wire
	}
	lower := strings.ToLower(client)
	if match, ok := r.byCase[lower]; ok {
		// A key present with an empty value is two client tools folded
		// together; the name as written is the only safe answer.
		if match != "" {
			return match, match != wire
		}
		return client, client != wire
	}
	if match, ok := r.byShape[foldToolName(client)]; ok && match != "" {
		return match, match != wire
	}
	return client, client != wire
}

// Restore maps a wire name back to the client name. See Resolve for the rules;
// a name it cannot map is returned as is.
func (r *ToolRenames) Restore(wire string) string {
	client, _ := r.Resolve(wire)
	return client
}

// ClientToolWireName builds the wire name for one client tool. The name is
// deliberately opaque: the free tier refuses a shaped body that still carries
// another client's vocabulary (Claude Code's ReportFindings and ScheduleWakeup
// together are enough, and dropping either name lifts the refusal), so no
// client tool name may reach the wire. The digest keeps one client tool on one
// wire name across requests, and collisions with used names get a _2, _3, …
// suffix. The chosen name is recorded in used.
func ClientToolWireName(name string, used map[string]bool) string {
	if used == nil {
		used = map[string]bool{}
	}
	digest := sha256.Sum256([]byte(name))
	clean := hex.EncodeToString(digest[:])[:clientToolDigestLength]
	max := toolWireNameLimit - len(ClientToolPrefix)
	if len(clean) > max {
		clean = clean[:max]
	}
	base := ClientToolPrefix + clean
	if !used[base] {
		used[base] = true
		return base
	}
	for suffix := 2; ; suffix++ {
		tag := "_" + strconv.Itoa(suffix)
		cut := toolWireNameLimit - len(tag)
		if cut > len(base) {
			cut = len(base)
		}
		candidate := base[:cut] + tag
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}
