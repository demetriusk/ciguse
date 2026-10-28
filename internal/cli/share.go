package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
)

// shareThread is the public thread where people post their export --share data, and
// shareThreadID its GraphQL id.
const (
	shareThread   = "https://github.com/demetriusk/ciguse/discussions/1"
	shareThreadID = "D_kwDOVZY6Es4AqmDo"
)

// shareMarker tags replies ciguse posted, so sharing again edits that reply and never
// touches the user's other replies.
const shareMarker = "<!-- ciguse share -->"

// gh runs the GitHub CLI with input on stdin. Replaced in tests.
var gh = runGH

func runGH(input string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		err = fmt.Errorf("gh: %s", strings.TrimSpace(string(ee.Stderr)))
	}
	return strings.TrimSpace(string(out)), err
}

// runShare posts the user's export --share as a reply in the data thread through the
// GitHub CLI, or edits the reply it posted before, only after the user says yes. Without
// a terminal or gh, it prints the data and where to post it.
func runShare(args []string, version string, stdout, stderr io.Writer) int {
	if _, ok := parse(newFlags("share", stderr), args, 0); !ok {
		return 2
	}
	s, code := openStore(stderr)
	if s == nil {
		return code
	}
	var data bytes.Buffer
	code = exportShare(s, &data, stderr)
	s.Close()
	if code != 0 {
		return code
	}
	if strings.Count(data.String(), "\n") < 2 {
		fmt.Fprintln(stdout, prefix, "nothing to share yet: it needs one full day since your first use")
		return 0
	}
	fmt.Fprint(stdout, data.String())
	manual := func(why string) int {
		fmt.Fprintf(stdout, "\n%s %s, so post it yourself: reply at %s with the lines above in a csv block\n", prefix, why, shareThread)
		return 0
	}
	if !canAsk() {
		return manual("not in a terminal")
	}
	login, err := gh("", "api", "user", "--jq", ".login")
	if err != nil {
		return manual("GitHub CLI isn't installed or signed in (gh auth login)")
	}
	reply, err := gh("", "api", "graphql", "--paginate", "-f", `query=query($endCursor: String) {
		node(id: "`+shareThreadID+`") { ... on Discussion { comments(first: 100, after: $endCursor) {
			pageInfo { hasNextPage endCursor } nodes { id viewerDidAuthor body } } } } }`,
		"--jq", `.data.node.comments.nodes[] | select(.viewerDidAuthor and (.body | contains("`+shareMarker+`"))) | .id`)
	if err != nil {
		return fail(stderr, err)
	}
	reply, _, _ = strings.Cut(reply, "\n")

	id, verb, mutation := shareThreadID, "posted", "addDiscussionComment(input: {discussionId: $id, body: $body})"
	question := fmt.Sprintf("Post this publicly to %s as @%s? Anyone can see it. [y/N] ", shareThread, login)
	if reply != "" {
		id, verb, mutation = reply, "updated", "updateDiscussionComment(input: {commentId: $id, body: $body})"
		question = fmt.Sprintf("Update your reply in %s as @%s? [y/N] ", shareThread, login)
	}
	fmt.Fprint(stdout, "\n"+bold.Render(question))
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	if !slices.Contains([]string{"y", "yes"}, strings.ToLower(strings.TrimSpace(line))) {
		fmt.Fprintln(stdout, prefix, "nothing posted")
		return 1
	}
	body := shareMarker + "\n```csv\n" + data.String() + "```\n<sub>ciguse " + version + "</sub>\n"
	url, err := gh(body, "api", "graphql", "-f", "query=mutation($id: ID!, $body: String!) { "+mutation+" { comment { url } } }",
		"-f", "id="+id, "-F", "body=@-", "--jq", ".data[].comment.url")
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, prefix, verb, url)
	return 0
}
