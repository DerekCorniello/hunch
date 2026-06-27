#!/usr/bin/env zsh
#
# Deterministic unit tests for the zsh integration's display-decision logic
# (integrations/zsh/hunch.zsh).
#
# The ghost-text flicker bugs that recur in this file all live in a handful of
# pure decision functions: given the current BUFFER, the cached suggestion, and
# what is currently in POSTDISPLAY, decide whether to paint, repaint from cache,
# leave a stale response alone, or clear. Those decisions do not need a live
# terminal or daemon to verify — they need controlled inputs and assertions on
# the resulting state.
#
# So this harness stubs the ZLE/coproc surface (zle, bindkey, add-zsh-hook,
# autoload, and the coprocess query), sources hunch.zsh to get the real
# function definitions, then drives the functions directly. Each scenario maps
# to a specific flicker/precedence regression from the project history.
#
# Run: zsh integrations/zsh/hunch_test.zsh   (exit 0 = pass, 1 = failure)

# ---- ZLE/coproc stubs (must exist before sourcing hunch.zsh) ---------------

# zle: report "not loaded" for -l queries so the source-time widget wiring is
# skipped; succeed silently for everything else (zle -N/-A/-F/-R, widget calls).
zle() {
	[[ "$1" == "-l" ]] && return 1
	return 0
}
# bindkey returns nothing, so the orig-widget capture finds no prior binding.
bindkey() { return 0; }
add-zsh-hook() { return 0; }
autoload() { return 0; }

# HUNCH_BIN=true makes _hunch_daemon_ensure a no-op at source time.
export HUNCH_BIN=true

source "${0:A:h}/hunch.zsh"

# Replace the real query (which would spawn a coprocess) with a capturing stub
# that records whether and with what prefix a query would have been sent. It
# mirrors the real function's _HUNCH_SENT side effect so _hunch_update's
# already-sent guard behaves identically.
typeset -g _Q_CALLED=0 _Q_PREFIX=""
_hunch_query() {
	_Q_CALLED=1
	_Q_PREFIX="$1"
	_HUNCH_SENT="$1"
}

# ---- test helpers ---------------------------------------------------------

typeset -i _fail=0

assert_eq() {
	local desc="$1" got="$2" want="$3"
	if [[ "$got" != "$want" ]]; then
		print -r -- "FAIL: $desc: got [$got] want [$want]"
		(( _fail++ ))
	else
		print -r -- "ok:   $desc"
	fi
}

reset_state() {
	BUFFER=""
	CURSOR=0
	POSTDISPLAY=""
	region_highlight=()
	_HUNCH_SUGGESTION=""
	_HUNCH_DISPLAY=""
	_HUNCH_SENT=$'\0'
	_HUNCH_CANDIDATES=()
	_HUNCH_CYCLE_IDX=0
	_Q_CALLED=0
	_Q_PREFIX=""
}

# feed_response delivers one coprocess response to _hunch_on_response via a real
# file descriptor, exactly as zle -F would. It builds the JSON line the serve
# binary would emit: the first argument is the echoed prefix, the rest are the
# ranked raw suggestions (none => empty array).
feed_response() {
	local prefix="$1" tmp fd raw
	shift
	_hunch_json_escape "$prefix"; local p=$REPLY
	local elems=()
	for raw in "$@"; do
		_hunch_json_escape "$raw"
		elems+=("\"$REPLY\"")
	done
	tmp=$(mktemp)
	print -r -- "{\"prefix\":\"$p\",\"raws\":[${(j.,.)elems}]}" >"$tmp"
	exec {fd}<"$tmp"
	# _hunch_on_response reads + parses, then dispatches to the applier widget
	# via `zle`. `zle` is stubbed in this harness, so invoke the applier
	# directly to exercise the display logic (where BUFFER is readable).
	_hunch_on_response "$fd"
	_hunch_apply_response
	exec {fd}<&-
	rm -f "$tmp"
}

# ---- scenarios ------------------------------------------------------------

# S1: empty buffer, daemon offers a suggestion -> show it in full.
reset_state
BUFFER="" CURSOR=0
feed_response "" "git status"
assert_eq "S1 empty-buffer shows full suggestion" "$POSTDISPLAY" "git status"
assert_eq "S1 display var set" "$_HUNCH_DISPLAY" "git status"
assert_eq "S1 suggestion cached" "$_HUNCH_SUGGESTION" "git status"

# S2: typed prefix, suggestion extends it -> show only the suffix, highlighted.
reset_state
BUFFER="git st" CURSOR=6
feed_response "git st" "git status"
assert_eq "S2 suffix-only ghost text" "$POSTDISPLAY" "atus"
assert_eq "S2 one highlight region" "${#region_highlight}" "1"

# S3: a late response for a prefix the user has already typed past must be
# dropped, leaving the current ghost text untouched (the core anti-flicker rule).
reset_state
BUFFER="git stat" CURSOR=8
POSTDISPLAY="us" _HUNCH_DISPLAY="us" _HUNCH_SUGGESTION="git status"
feed_response "git st" "git status"
assert_eq "S3 stale response ignored (POSTDISPLAY)" "$POSTDISPLAY" "us"
assert_eq "S3 stale response ignored (display)" "$_HUNCH_DISPLAY" "us"

# S4: a response with no suggestion clears hunch's own ghost text.
reset_state
BUFFER="zzz" CURSOR=3
POSTDISPLAY="old" _HUNCH_DISPLAY="old" _HUNCH_SUGGESTION="zzzold"
feed_response "zzz"
assert_eq "S4 empty suggestion clears POSTDISPLAY" "$POSTDISPLAY" ""
assert_eq "S4 empty suggestion clears display" "$_HUNCH_DISPLAY" ""

# S5: while typing into a cached suggestion, repaint instantly from cache and
# do NOT re-query (this is what stops the every-keystroke flicker).
reset_state
_HUNCH_SUGGESTION="git status" _HUNCH_SENT="git s"
BUFFER="git s" CURSOR=5
_hunch_update
assert_eq "S5 repaint suffix from cache" "$POSTDISPLAY" "tatus"
assert_eq "S5 no re-query when buffer already sent" "$_Q_CALLED" "0"

# S6: clearing must not wipe POSTDISPLAY if another plugin owns it now.
reset_state
_HUNCH_DISPLAY="atus" _HUNCH_SUGGESTION="git status"
POSTDISPLAY="other-plugin-text"
_hunch_clear_display
assert_eq "S6 foreign POSTDISPLAY preserved" "$POSTDISPLAY" "other-plugin-text"
assert_eq "S6 hunch display state reset" "$_HUNCH_DISPLAY" ""

# S7: accepting completes the buffer with the ghost suffix.
reset_state
BUFFER="git st" CURSOR=6
_HUNCH_DISPLAY="atus" POSTDISPLAY="atus" _HUNCH_SUGGESTION="git status"
_hunch_accept_full
assert_eq "S7 buffer completed" "$BUFFER" "git status"
assert_eq "S7 cursor at end" "$CURSOR" "10"
assert_eq "S7 display cleared after accept" "$_HUNCH_DISPLAY" ""

# S8: ownership gate — accept keys only act when hunch is the plugin currently
# showing the ghost text and the cursor is at end of buffer.
reset_state
BUFFER="git st" CURSOR=6 _HUNCH_DISPLAY="atus" POSTDISPLAY="atus"
_hunch_owns_suggestion && r=yes || r=no
assert_eq "S8 owns when POSTDISPLAY matches and cursor at end" "$r" "yes"
POSTDISPLAY="something-else"
_hunch_owns_suggestion && r=yes || r=no
assert_eq "S8 not owns when another plugin owns POSTDISPLAY" "$r" "no"
POSTDISPLAY="atus" CURSOR=3
_hunch_owns_suggestion && r=yes || r=no
assert_eq "S8 not owns when cursor mid-buffer" "$r" "no"

# S9: a brand-new buffer fires exactly one query for that buffer.
reset_state
BUFFER="git c" CURSOR=5
_hunch_update
assert_eq "S9 query fired for new buffer" "$_Q_CALLED" "1"
assert_eq "S9 query carries the buffer as prefix" "$_Q_PREFIX" "git c"

# S10: exit-code -> outcome mapping, including the signal-neutral range.
_hunch_outcome 0
assert_eq "S10 exit 0 is success" "$REPLY" "success"
_hunch_outcome 1
assert_eq "S10 exit 1 is failure" "$REPLY" "failure"
_hunch_outcome 127
assert_eq "S10 exit 127 is failure" "$REPLY" "failure"
_hunch_outcome 130
assert_eq "S10 Ctrl-C (130) is neutral" "$REPLY" ""
_hunch_outcome 143
assert_eq "S10 SIGTERM (143) is neutral" "$REPLY" ""

# S11: JSON escape/unescape round-trips strings with the tricky characters
# (backslash, quote, tab, newline) that motivated JSON framing.
tricky=$'a\\b"c\td\ne'
_hunch_json_escape "$tricky"; escaped=$REPLY
# The escaped form must be a single physical line.
assert_eq "S11 escaped form has no real newline" "${escaped[(I)$'\n']}" "0"
_hunch_json_unescape "$escaped"; roundtripped=$REPLY
assert_eq "S11 escape/unescape round-trips" "$roundtripped" "$tricky"

# S12: parsing a response whose raw values contain escaped quotes and commas
# recovers each exact original string from the array.
_hunch_json_escape 'git st'; ep=$REPLY
_hunch_json_escape 'git commit -m "a, b"'; er1=$REPLY
_hunch_json_escape 'git commit --amend'; er2=$REPLY
_hunch_parse_response "{\"prefix\":\"$ep\",\"raws\":[\"$er1\",\"$er2\"]}"
assert_eq "S12 parse recovers prefix" "$_HUNCH_RESP_PREFIX" "git st"
assert_eq "S12 parse recovers raws count" "${#_HUNCH_RESP_RAWS}" "2"
assert_eq "S12 parse recovers raw 1 (quotes/comma)" "$_HUNCH_RESP_RAWS[1]" 'git commit -m "a, b"'
assert_eq "S12 parse recovers raw 2" "$_HUNCH_RESP_RAWS[2]" 'git commit --amend'

# S12b: an empty array parses to no candidates.
_hunch_parse_response '{"prefix":"x","raws":[]}'
assert_eq "S12b empty raws array" "${#_HUNCH_RESP_RAWS}" "0"

# S13: a built request is valid, parseable JSON carrying the buffer prefix,
# the state array, and the current directory.
reset_state
_HUNCH_PREV=("git add ." "git status")
_hunch_build_request "git p"
req=$REPLY
[[ "$req" == *'"prefix":"git p"'* ]] && r=yes || r=no
assert_eq "S13 request carries prefix" "$r" "yes"
[[ "$req" == *'"cwd":"'* ]] && r=yes || r=no
assert_eq "S13 request carries cwd field" "$r" "yes"
[[ "$req" == *'"state":["git add .","git status"]'* ]] && r=yes || r=no
assert_eq "S13 request carries state array" "$r" "yes"

# S14: cycling steps through the candidate list and wraps around.
reset_state
BUFFER="git " CURSOR=4
feed_response "git " "git status" "git stash" "git show"
assert_eq "S14 first candidate shown" "$POSTDISPLAY" "status"
assert_eq "S14 three candidates" "${#_HUNCH_CANDIDATES}" "3"
_hunch_cycle_next
assert_eq "S14 next -> second" "$POSTDISPLAY" "stash"
_hunch_cycle_next
assert_eq "S14 next -> third" "$POSTDISPLAY" "show"
_hunch_cycle_next
assert_eq "S14 next wraps to first" "$POSTDISPLAY" "status"
_hunch_cycle_prev
assert_eq "S14 prev wraps to third" "$POSTDISPLAY" "show"

# S15: cycle keys are inert when hunch does not own the on-screen suggestion.
reset_state
BUFFER="git " CURSOR=4
feed_response "git " "git status" "git stash"
POSTDISPLAY="foreign" # another plugin took over
_hunch_cycle_next
assert_eq "S15 cycle inert when not owning POSTDISPLAY" "$POSTDISPLAY" "foreign"

# ---- result ---------------------------------------------------------------

if (( _fail )); then
	print -r -- "FAILED: $_fail assertion(s)"
	exit 1
fi
print -r -- "all zsh integration tests passed"
