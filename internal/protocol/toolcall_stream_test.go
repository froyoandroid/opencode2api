package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"opencode2api/internal/jsonutil"
)

func chatRenamer() *streamToolRenamer {
	r := NewToolRenames()
	r.Add("Read", "clint_bbbbbbbbbbbb")
	return &streamToolRenamer{protocol: Chat, renames: r, tools: map[string]*streamToolRenameState{}}
}

func firstCallID(t *testing.T, raw string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("rewritten frame is not JSON: %s: %v", raw, err)
	}
	choice, _ := jsonutil.SliceAt(payload, "choices")[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	calls := jsonutil.SliceAt(delta, "tool_calls")
	if len(calls) == 0 {
		return ""
	}
	call, _ := calls[0].(map[string]any)
	return jsonutil.StringAt(call, "id")
}

// An upstream may stream a tool call whose id never arrives, or only on some
// fragments. A strict OpenAI client aborts the turn on a fragment without one,
// so the renamer mints an id on the naming fragment and repeats it.
func TestStreamMintsMissingToolCallID(t *testing.T) {
	renamer := chatRenamer()
	first := firstCallID(t, string(renamer.rewriteFrame(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"clint_bbbbbbbbbbbb","arguments":""}}]}}]}`)))
	if !strings.HasPrefix(first, "call_") {
		t.Fatalf("naming fragment carried no id, got %q", first)
	}
	second := firstCallID(t, string(renamer.rewriteFrame(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}`)))
	if second != first {
		t.Fatalf("continuation id %q differs from the start id %q", second, first)
	}
}

// The reported failure shape: tool_calls[0] with an id, name and arguments all
// null. It is not a start and not a continuation, so a client cannot use it.
func TestStreamDropsEmptyToolCallEntry(t *testing.T) {
	renamer := chatRenamer()
	renamer.rewriteFrame(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"name":"clint_bbbbbbbbbbbb","arguments":""}}]}}]}`)
	out := string(renamer.rewriteFrame(
		`{"choices":[{"delta":{"content":null,"tool_calls":[{"index":1,"id":null,"function":{"name":null,"arguments":null}}]}}]}`))
	if strings.Contains(out, `"name":null`) || strings.Contains(out, `"id":null`) {
		t.Fatalf("an unusable tool-call entry reached the client: %s", out)
	}
	// The replaced array is empty rather than absent: an empty list is a
	// valid delta and costs a strict client nothing.
	if strings.Contains(out, `"index":1`) {
		t.Fatalf("the dropped entry survived: %s", out)
	}
}

// A frame carrying no tool call of its own is passed through untouched, so the
// rewriter stays cheap on ordinary text deltas.
func TestStreamLeavesUnrelatedFrameAlone(t *testing.T) {
	renamer := chatRenamer()
	raw := `{"choices":[{"delta":{"content":"hello"}}]}`
	if out := string(renamer.rewriteFrame(raw)); out != raw {
		t.Fatalf("a text delta was rewritten: %s", out)
	}
}
