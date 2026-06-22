_HUNCH_BIN=${HUNCH_BIN:-hunch}
typeset -ga _HUNCH_PREV
_HUNCH_PREV=("" "")
_HUNCH_SUGGESTION=""
_HUNCH_LAST_BUFFER=""
_HUNCH_LAST_PREFIX=""
_HUNCH_LAST_POSTDISPLAY=""
_HUNCH_LAST_SUGGESTION=""
_HUNCH_HIGHLIGHT_STYLE=${HUNCH_HIGHLIGHT_STYLE:-${ZSH_AUTOSUGGEST_HIGHLIGHT_STYLE:-fg=8}}

_hunch_clear_display() {
	region_highlight=("${region_highlight[@]:#*memo=hunch*}")
	# Only clear POSTDISPLAY if it still holds what hunch put there. Another
	# plugin (e.g. zsh-autosuggestions) may have set its own suggestion since;
	# wiping POSTDISPLAY unconditionally would erase that suggestion instead.
	if [[ "$POSTDISPLAY" == "$_HUNCH_LAST_POSTDISPLAY" ]]; then
		POSTDISPLAY=""
	fi
	_HUNCH_LAST_PREFIX=""
	_HUNCH_LAST_POSTDISPLAY=""
	_HUNCH_LAST_SUGGESTION=""
}

_hunch_show_display() {
	local prefix="$1"
	local display="$2"
	local suggestion="$3"

	# Hunch's suggestion takes precedence over another plugin's (e.g.
	# zsh-autosuggestions) when hunch has one to offer, so always set it here.
	# _hunch_clear_display is the one that holds back when hunch has nothing.
	if [[ "$display" == "$_HUNCH_LAST_POSTDISPLAY" && "$suggestion" == "$_HUNCH_LAST_SUGGESTION" && "$POSTDISPLAY" == "$_HUNCH_LAST_POSTDISPLAY" ]]; then
		_HUNCH_LAST_PREFIX="$prefix"
		return
	fi

	_HUNCH_LAST_PREFIX="$prefix"
	POSTDISPLAY="$display"
	_HUNCH_LAST_POSTDISPLAY="$display"
	_HUNCH_LAST_SUGGESTION="$suggestion"
	region_highlight=("${region_highlight[@]:#*memo=hunch*}")
	region_highlight+=("$CURSOR $((CURSOR + $#display)) $_HUNCH_HIGHLIGHT_STYLE memo=hunch")
}

_hunch_daemon_ensure() {
	if ! "$_HUNCH_BIN" daemon status >/dev/null 2>&1; then
		"$_HUNCH_BIN" daemon start >/dev/null 2>&1
	fi
}
_hunch_daemon_ensure

_hunch_record() {
	local cmd="${history[$((HISTCMD-1))]}"
	[[ -z "$cmd" ]] && return

	"$_HUNCH_BIN" client record \
		--state "${(j.,.)_HUNCH_PREV}" \
		--next "$cmd" \
		--at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		>/dev/null 2>&1 &!

	_HUNCH_PREV[1]=$_HUNCH_PREV[2]
	_HUNCH_PREV[2]=$cmd
}

_hunch_predict() {
	if [[ -n "$_HUNCH_LAST_SUGGESTION" && "$BUFFER" == "$_HUNCH_LAST_SUGGESTION" ]]; then
		_hunch_clear_display
		return
	fi

	local suggestion display
	if [[ -z "$BUFFER" ]]; then
		suggestion=$("$_HUNCH_BIN" client predict \
			--state "${(j.,.)_HUNCH_PREV}" \
			--limit 1 \
			--raw 2>/dev/null)

		if [[ -n "$suggestion" ]]; then
			display="$suggestion"
		else
			display=""
		fi
		_HUNCH_LAST_BUFFER="$BUFFER"
		if [[ -n "$display" ]]; then
			_hunch_show_display "$BUFFER" "$display" "$suggestion"
		else
			_hunch_clear_display
		fi
		return
	fi

	if [[ "$BUFFER" == "$_HUNCH_LAST_BUFFER" ]]; then
		# No need to re-query the daemon, but another plugin's async callback
		# (e.g. zsh-autosuggestions, which fetches suggestions via a forked
		# process and delivers them through a zle -F handler well after this
		# hook last ran) may have overwritten POSTDISPLAY since we set it.
		# Reassert our cached suggestion so it doesn't flicker away every
		# other redraw.
		if [[ -n "$_HUNCH_LAST_POSTDISPLAY" && "$POSTDISPLAY" != "$_HUNCH_LAST_POSTDISPLAY" ]]; then
			_hunch_show_display "$_HUNCH_LAST_PREFIX" "$_HUNCH_LAST_POSTDISPLAY" "$_HUNCH_LAST_SUGGESTION"
		fi
		return
	fi
	_HUNCH_LAST_BUFFER="$BUFFER"

	suggestion=$("$_HUNCH_BIN" client predict \
		--state "${(j.,.)_HUNCH_PREV}" \
		--prefix "$BUFFER" \
		--limit 1 \
		--raw 2>/dev/null)

	if [[ -n "$suggestion" ]]; then
		display="${suggestion#$BUFFER}"
		if [[ "$display" == "$suggestion" ]]; then
			display=""
		fi
	else
		display=""
	fi

	if [[ -n "$display" ]]; then
		_hunch_show_display "$BUFFER" "$display" "$suggestion"
	else
		_hunch_clear_display
	fi
}

_hunch_accept_or_forward() {
	# Only treat this as an accept if hunch is the one currently showing the
	# suggestion (POSTDISPLAY matches what hunch set). Otherwise some other
	# plugin (e.g. zsh-autosuggestions) owns the suggestion right now, and we
	# must defer to whatever widget was originally bound to this key.
	if [[ CURSOR -eq ${#BUFFER} && -n "$POSTDISPLAY" && "$POSTDISPLAY" == "$_HUNCH_LAST_POSTDISPLAY" && "$BUFFER" == "$_HUNCH_LAST_PREFIX" ]]; then
		local suffix="$POSTDISPLAY"
		_hunch_clear_display
		BUFFER="$BUFFER$suffix"
		CURSOR=${#BUFFER}
		_HUNCH_LAST_BUFFER=""
		if (( $+functions[_zsh_highlight] )); then
			_zsh_highlight
		fi
	else
		local key="$KEYMAP:^[[C"
		zle "${_HUNCH_ORIG_WIDGET[$key]:-.forward-char}"
	fi
}

_hunch_accept_end() {
	if [[ -n "$POSTDISPLAY" && "$POSTDISPLAY" == "$_HUNCH_LAST_POSTDISPLAY" && "$BUFFER" == "$_HUNCH_LAST_PREFIX" ]]; then
		local suffix="$POSTDISPLAY"
		_hunch_clear_display
		BUFFER="$BUFFER$suffix"
		CURSOR=${#BUFFER}
		_HUNCH_LAST_BUFFER=""
		if (( $+functions[_zsh_highlight] )); then
			_zsh_highlight
		fi
	else
		local key="$KEYMAP:^[[F"
		zle "${_HUNCH_ORIG_WIDGET[$key]:-.end-of-line}"
	fi
}

_hunch_line_finish() {
	_hunch_clear_display
	_HUNCH_SUGGESTION=""
	_HUNCH_LAST_BUFFER=""
}

zle -N _hunch_accept_or_forward
zle -N _hunch_accept_end
zle -N _hunch_predict
zle -N _hunch_line_finish

typeset -g _HUNCH_HOOKS_INSTALLED=0

if zle -l zle-line-init 2>/dev/null; then
  zle -A zle-line-init _hunch_orig_line_init
fi

_hunch_on_line_init() {
  if zle -l _hunch_orig_line_init 2>/dev/null; then
    zle _hunch_orig_line_init
  fi

  if (( ! _HUNCH_HOOKS_INSTALLED )); then
    _HUNCH_HOOKS_INSTALLED=1
    if zle -l zle-line-pre-redraw 2>/dev/null; then
      zle -A zle-line-pre-redraw _hunch_orig_pre_redraw
    fi
    zle -N zle-line-pre-redraw _hunch_pre_redraw
  fi

  _hunch_predict
}

zle -N zle-line-init _hunch_on_line_init
zle -N zle-line-finish _hunch_line_finish

_hunch_pre_redraw() {
  if zle -l _hunch_orig_pre_redraw 2>/dev/null; then
    zle _hunch_orig_pre_redraw
  fi
  _hunch_predict
}

# Remember whatever widget each keymap already had bound to these keys (e.g.
# zsh-autosuggestions' own accept-on-arrow wrapper) so that when hunch has no
# suggestion of its own to offer, it forwards to that widget instead of a
# raw builtin, rather than silently replacing the existing behavior.
typeset -gA _HUNCH_ORIG_WIDGET

_hunch_capture_orig_widget() {
	local keymap="$1" keys="$2" line widget
	line=$(bindkey -M "$keymap" "$keys" 2>/dev/null)
	[[ -z "$line" ]] && return
	widget="${line#* }"
	[[ -n "$widget" && "$widget" != "undefined-key" ]] && _HUNCH_ORIG_WIDGET[$keymap:$keys]="$widget"
}

for _hunch_km in main vicmd viins; do
	_hunch_capture_orig_widget "$_hunch_km" '^[[C'
	_hunch_capture_orig_widget "$_hunch_km" '^[[F'
done
unset _hunch_km

bindkey '^[[C' _hunch_accept_or_forward
bindkey '^[[F' _hunch_accept_end

bindkey -M vicmd '^[[C' _hunch_accept_or_forward
bindkey -M vicmd '^[[F' _hunch_accept_end

bindkey -M viins '^[[C' _hunch_accept_or_forward
bindkey -M viins '^[[F' _hunch_accept_end

autoload -Uz add-zsh-hook
add-zsh-hook precmd _hunch_record
