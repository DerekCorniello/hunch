_HUNCH_BIN=${HUNCH_BIN:-hunch}

typeset -ga _HUNCH_PREV
_HUNCH_PREV=("" "")
_HUNCH_SUGGESTION=""
_HUNCH_LAST_BUFFER=""

_hunch_daemon_ensure() {
	if ! "$_HUNCH_BIN" daemon status >/dev/null 2>&1; then
		"$_HUNCH_BIN" daemon start >/dev/null 2>&1 &
	fi
}
_hunch_daemon_ensure

_hunch_record() {
	local exit_code=$?
	local cmd="${history[$((HISTCMD-1))]}"
	[[ -z "$cmd" ]] && return

	local outcome="success"
	[[ $exit_code -ne 0 ]] && outcome="failure"

	"$_HUNCH_BIN" client record \
		--state "${(j.,.)_HUNCH_PREV}" \
		--next "$cmd" \
		--outcome "$outcome" \
		--cwd "$PWD" \
		--at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		>/dev/null 2>&1 &
	disown

	_HUNCH_PREV[1]=$_HUNCH_PREV[2]
	_HUNCH_PREV[2]=$cmd
}

_hunch_predict() {
	[[ -z "$BUFFER" ]] && { POSTDISPLAY=""; return }
	[[ "$BUFFER" == "$_HUNCH_LAST_BUFFER" ]] && return
	_HUNCH_LAST_BUFFER="$BUFFER"

	local suggestion
	suggestion=$("$_HUNCH_BIN" client predict \
		--state "${(j.,.)_HUNCH_PREV}" \
		--prefix "$BUFFER" \
		--limit 1 \
		--template 2>/dev/null)

	if [[ -n "$suggestion" && "$suggestion" != "$BUFFER" ]]; then
		POSTDISPLAY="${suggestion#$BUFFER}"
	else
		POSTDISPLAY=""
	fi
}

_hunch_accept_or_forward() {
	if [[ CURSOR -eq ${#BUFFER} && -n "$POSTDISPLAY" ]]; then
		BUFFER="$BUFFER$POSTDISPLAY"
		CURSOR=${#BUFFER}
		POSTDISPLAY=""
	else
		zle .forward-char
	fi
}

_hunch_accept_end() {
	if [[ -n "$POSTDISPLAY" ]]; then
		BUFFER="$BUFFER$POSTDISPLAY"
		CURSOR=${#BUFFER}
		POSTDISPLAY=""
	fi
}

_hunch_self_insert() {
	zle .self-insert
	_hunch_predict
}

_hunch_backward_delete() {
	zle .backward-delete-char
	_HUNCH_LAST_BUFFER=""
	_hunch_predict
}

_hunch_line_finish() {
	POSTDISPLAY=""
	_HUNCH_SUGGESTION=""
	_HUNCH_LAST_BUFFER=""
}

zle -N _hunch_self_insert
zle -N _hunch_backward_delete
zle -N _hunch_accept_or_forward
zle -N _hunch_accept_end
zle -N zle-line-finish _hunch_line_finish

bindkey '^[[C' _hunch_accept_or_forward
bindkey '^[[F' _hunch_accept_end

bindkey -M vicmd '^[[C' _hunch_accept_or_forward
bindkey -M vicmd '^[[F' _hunch_accept_end

bindkey -M viins '^[[C' _hunch_accept_or_forward
bindkey -M viins '^[[F' _hunch_accept_end

autoload -Uz add-zsh-hook
add-zsh-hook precmd _hunch_record

zle -N self-insert _hunch_self_insert
zle -N backward-delete-char _hunch_backward_delete
