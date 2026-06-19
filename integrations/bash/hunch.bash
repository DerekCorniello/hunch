_HUNCH_BIN=${HUNCH_BIN:-hunch}
_HUNCH_PREV1=""
_HUNCH_PREV2=""
_HUNCH_SUGGESTION=""

_hunch_daemon_ensure() {
	if ! "$_HUNCH_BIN" daemon status >/dev/null 2>&1; then
		"$_HUNCH_BIN" daemon start >/dev/null 2>&1 &
	fi
}
_hunch_daemon_ensure

_hunch_record() {
	local exit_code=$?
	local cmd
	cmd=$(history 1 | sed 's/^ *[0-9]* *//')
	[[ -z "$cmd" ]] && return

	"$_HUNCH_BIN" client record \
		--state "$_HUNCH_PREV1,$_HUNCH_PREV2" \
		--next "$cmd" \
		--at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		>/dev/null 2>&1 &
	disown

	_HUNCH_PREV1=$_HUNCH_PREV2
	_HUNCH_PREV2=$cmd
}

_hunch_predict() {
	_HUNCH_SUGGESTION=$("$_HUNCH_BIN" client predict \
		--state "$_HUNCH_PREV1,$_HUNCH_PREV2" \
		--prefix "$READLINE_LINE" \
		--limit 1 \
		--template 2>/dev/null)

	if [[ -z "$_HUNCH_SUGGESTION" || "$_HUNCH_SUGGESTION" == "${READLINE_LINE:-}" ]]; then
		_HUNCH_SUGGESTION=""
	fi
}

_hunch_accept() {
	_hunch_predict
	if [[ -n "$_HUNCH_SUGGESTION" ]]; then
		READLINE_LINE="$_HUNCH_SUGGESTION"
		READLINE_POINT=${#READLINE_LINE}
	fi
}

if [[ -z "$PROMPT_COMMAND" ]]; then
	PROMPT_COMMAND="_hunch_record"
elif [[ "$PROMPT_COMMAND" != *"_hunch_record"* ]]; then
	PROMPT_COMMAND="_hunch_record; $PROMPT_COMMAND"
fi

bind -x '"\t": _hunch_accept'
