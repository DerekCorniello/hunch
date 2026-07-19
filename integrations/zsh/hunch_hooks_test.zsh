#!/usr/bin/env zsh
#
# Verifies that hunch composes on zle hooks instead of displacing other
# plugins.
#
# Binding a hook with plain `zle -N zle-line-pre-redraw` replaces whatever was
# bound before, with no error. That failure mode is silent in both directions:
# hunch would disable another plugin's hook, and a plugin loaded after hunch
# would disable hunch's ghost text. Neither shows up as anything except a
# feature quietly not working, so it needs a test rather than a code reading.
#
# Unlike hunch_test.zsh, this harness does not stub zle: the whole point is to
# exercise the real widget registration.
#
# Run: zsh integrations/zsh/hunch_hooks_test.zsh   (exit 0 = pass)

emulate -L zsh
zmodload zsh/zle 2>/dev/null || { print "zsh/zle unavailable, skipping"; exit 0 }
autoload -Uz +X add-zle-hook-widget 2>/dev/null
if (( ! ${+functions[add-zle-hook-widget]} )); then
	print "add-zle-hook-widget unavailable (zsh < 5.3), skipping"
	exit 0
fi

typeset -g fail=0
check_contains() {
	local label=$1 haystack=$2 needle=$3
	if [[ $haystack == *$needle* ]]; then
		print "ok:   $label"
	else
		print "FAIL: $label"
		print "      expected to find '$needle' in: ${haystack:-<empty>}"
		fail=1
	fi
}
check_eq() {
	if [[ $2 == $3 ]]; then
		print "ok:   $1"
	else
		print "FAIL: $1 (got '$2', want '$3')"
		fail=1
	fi
}
hook_widgets() { add-zle-hook-widget -L "$1" 2>/dev/null }

# A plugin that bound the hooks directly, the way zsh-autosuggestions and
# friends do, before hunch is ever sourced.
plugin_pre_redraw() { : }
plugin_line_init() { : }
plugin_line_finish() { : }
zle -N zle-line-pre-redraw plugin_pre_redraw
zle -N zle-line-init plugin_line_init
zle -N zle-line-finish plugin_line_finish

# HUNCH_BIN=true keeps the source-time daemon check from doing anything.
export HUNCH_BIN=true
source "${0:A:h}/hunch.zsh"

# Stub the parts that would need a daemon or a live line editor. Done after
# sourcing so the real definitions are replaced, not shadowed.
_hunch_start_coproc() { : }
_hunch_update() { : }
_hunch_clear_display() { : }

check_eq "add-zle-hook-widget is in use" "$_HUNCH_USING_ZLE_HOOK" "1"

# line-init and line-finish are bound at source time. Both must now run the
# plugin's widget and hunch's.
check_contains "line-init keeps the pre-existing widget" "$(hook_widgets line-init)" "plugin_line_init"
check_contains "line-init runs hunch" "$(hook_widgets line-init)" "_hunch_on_line_init"
check_contains "line-finish keeps the pre-existing widget" "$(hook_widgets line-finish)" "plugin_line_finish"
check_contains "line-finish runs hunch" "$(hook_widgets line-finish)" "_hunch_line_finish"

# pre-redraw is bound on the first line rather than at source time, so that
# hunch runs after any plugin sourced later. Until then the plugin's direct
# binding is untouched.
check_eq "pre-redraw is not bound before the first line" "$(hook_widgets line-pre-redraw)" ""

# Simulate the first line, which triggers the deferred binding.
_hunch_install_hook line-pre-redraw _hunch_pre_redraw _hunch_orig_pre_redraw
check_contains "pre-redraw adopts the pre-existing widget" "$(hook_widgets line-pre-redraw)" "plugin_pre_redraw"
check_contains "pre-redraw runs hunch" "$(hook_widgets line-pre-redraw)" "_hunch_pre_redraw"

# A plugin registering after hunch must not displace it.
late_pre_redraw() { : }
add-zle-hook-widget line-pre-redraw late_pre_redraw
typeset -g listed="$(hook_widgets line-pre-redraw)"
check_contains "a later plugin coexists with hunch" "$listed" "late_pre_redraw"
check_contains "hunch survives a later plugin" "$listed" "_hunch_pre_redraw"
check_contains "the original survives a later plugin" "$listed" "plugin_pre_redraw"

# Under the hook list the runner calls the predecessor itself, so hunch must
# not call it again or it would fire twice per redraw.
typeset -g orig_calls=0
_hunch_orig_pre_redraw() { (( orig_calls++ )) }
zle -N _hunch_orig_pre_redraw
_hunch_call_orig _hunch_orig_pre_redraw
check_eq "hook mode does not re-call the predecessor" "$orig_calls" "0"

# The legacy branch, used on zsh < 5.3 where there is no widget list. It can
# only chain one predecessor, but it must at least save it rather than drop it.
# A synthetic hook name keeps this from disturbing the real hooks asserted above.
legacy_predecessor() { : }
zle -N zle-hunch-test-hook legacy_predecessor
_HUNCH_USING_ZLE_HOOK=0
_hunch_install_hook hunch-test-hook _hunch_pre_redraw _hunch_legacy_orig
check_eq "fallback binds hunch" "${widgets[zle-hunch-test-hook]}" "user:_hunch_pre_redraw"
check_eq "fallback saves the predecessor" "${widgets[_hunch_legacy_orig]}" "user:legacy_predecessor"
_HUNCH_USING_ZLE_HOOK=1

if (( fail )); then
	print "FAILURES"
	exit 1
fi
print "all hook composition tests passed"
