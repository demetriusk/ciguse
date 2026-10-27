#!/bin/sh
# Records docs/claude-code.gif and docs/codex.gif: cig typed in each agent.
# Needs vhs and ffmpeg, plus Claude Code and Codex signed in, with the ciguse plugins
# installed, their hooks trusted, and /tmp/ciguse-demo/api-server trusted in both. Logs go
# to a throwaway data folder. Codex runs with --no-daemon: its shared daemon runs hooks with
# the environment it started with, and must not keep this script's CIGUSE_HOME.
# Run from the repository root: sh docs/demo/record-agents.sh
set -eu

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export DEMO_DATA="$work/data" DEMO_PROJECT=/tmp/ciguse-demo/api-server # a path with no user name in it
mkdir -p "$DEMO_PROJECT"
git -C "$DEMO_PROJECT" init -q
CIGUSE_HOME="$DEMO_DATA" ciguse init --product iqos >/dev/null
for ago in 40m 25m 10m; do CIGUSE_HOME="$DEMO_DATA" ciguse use --ago "$ago" >/dev/null; done

vhs -q docs/demo/claude.tape -o "$work/claude.gif"
vhs -q docs/demo/codex.tape -o "$work/codex.gif"

# gif IN OUT TOP: drop the rows above TOP, where each agent's banner names the account's
# plan and model, then encode with a small palette like docs/demo.gif.
gif() {
	ffmpeg -v error -y -i "$1" -filter_complex \
		"crop=iw:ih-$3:0:$3,fps=12,split[s][t];[s]palettegen=stats_mode=diff:max_colors=64[p];[t][p]paletteuse=dither=none:diff_mode=rectangle" "$2"
	echo "wrote $2 ($(du -h "$2" | cut -f1))"
}
gif "$work/claude.gif" docs/claude-code.gif "${CLAUDE_TOP:-160}"
gif "$work/codex.gif" docs/codex.gif "${CODEX_TOP:-0}"
