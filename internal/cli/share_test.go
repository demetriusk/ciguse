package cli

import (
	"errors"
	"strings"
	"testing"
)

// fakeGH stands in for the GitHub CLI: signed in as octo, with reply as the id of a reply
// ciguse posted before ("" for none). It returns the list of calls, each its args then stdin.
func fakeGH(t *testing.T, signedIn bool, reply string) *[]string {
	t.Helper()
	var calls []string
	gh = func(input string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call+"\n"+input)
		switch {
		case !signedIn:
			return "", errors.New("gh: not logged in")
		case strings.HasPrefix(call, "api user"):
			return "octo", nil
		case strings.Contains(call, "viewerDidAuthor"):
			return reply, nil
		case strings.Contains(call, "addDiscussionComment"):
			return shareThread + "#discussioncomment-1", nil
		case strings.Contains(call, "updateDiscussionComment"):
			return shareThread + "#discussioncomment-" + reply, nil
		}
		t.Fatalf("unexpected gh call: %s", call)
		return "", nil
	}
	t.Cleanup(func() { gh = runGH })
	return &calls
}

func TestShare(t *testing.T) {
	_, clock := setup(t)
	calls := fakeGH(t, true, "")
	out, code := run(t, "share")
	if code != 0 || out != "ciguse · nothing to share yet: it needs one full day since your first use\n" {
		t.Fatalf("exit %d: %s", code, out)
	}

	today := *clock
	*clock = today.AddDate(0, 0, -1)
	run(t, "use")
	*clock = today
	run(t, "use")
	data := "date,hours_working,hours_waiting,uses_working,uses_waiting,uses_away,nicotine_mg,hours_claude,uses_claude,hours_codex,uses_codex\n" +
		"2026-10-26,0.00,0.00,0,0,1,1.0,0.00,0,0.00,0\n"

	// Without a terminal there's no one to say yes, so it only says where to post.
	out, code = run(t, "share")
	if code != 0 || out != data+"\nciguse · not in a terminal, so post it yourself: reply at "+shareThread+" with the lines above in a csv block\n" {
		t.Fatalf("exit %d: %s", code, out)
	}
	if len(*calls) != 0 {
		t.Fatalf("gh called without a terminal: %q", *calls)
	}

	out, code = ask(t, "n\n", "share")
	if code != 1 || !strings.HasPrefix(out, data) ||
		!strings.Contains(out, "Post this publicly to "+shareThread+" as @octo? Anyone can see it. [y/N] ") ||
		!strings.HasSuffix(out, "ciguse · nothing posted\n") {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, c := range *calls {
		if strings.Contains(c, "mutation") {
			t.Fatalf("posted after no: %s", c)
		}
	}

	out, code = ask(t, "y\n", "share")
	post := (*calls)[len(*calls)-1]
	if code != 0 || !strings.HasSuffix(out, "ciguse · posted "+shareThread+"#discussioncomment-1\n") ||
		!strings.Contains(post, "addDiscussionComment") || !strings.Contains(post, "id="+shareThreadID) ||
		!strings.HasSuffix(post, "\n"+shareMarker+"\n```csv\n"+data+"```\n<sub>ciguse test</sub>\n") {
		t.Fatalf("exit %d: %s\ncall: %s", code, out, post)
	}

	// A second share edits the reply ciguse posted before.
	calls = fakeGH(t, true, "DC_7")
	out, code = ask(t, "yes\n", "share")
	post = (*calls)[len(*calls)-1]
	if code != 0 || !strings.Contains(out, "Update your reply in "+shareThread+" as @octo? [y/N] ") ||
		!strings.HasSuffix(out, "ciguse · updated "+shareThread+"#discussioncomment-DC_7\n") ||
		!strings.Contains(post, "updateDiscussionComment") || !strings.Contains(post, "id=DC_7") {
		t.Fatalf("exit %d: %s\ncall: %s", code, out, post)
	}

	fakeGH(t, false, "")
	out, code = ask(t, "y\n", "share")
	if code != 0 || !strings.HasSuffix(out, "ciguse · GitHub CLI isn't installed or signed in (gh auth login), so post it yourself: reply at "+shareThread+" with the lines above in a csv block\n") {
		t.Fatalf("exit %d: %s", code, out)
	}
}
