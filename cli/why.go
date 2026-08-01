package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/DerekCorniello/hunch/ipc"
)

// cmdWhy is the human-readable counterpart to `hunch client explain`: it
// shows which fallback context answered a prediction and the per-candidate
// scoring breakdown, so a suggestion that looks wrong can be diagnosed
// instead of just distrusted. With no --state, it defaults to the last two
// commands in ~/.zsh_history and the current directory, so running `hunch
// why` right after a bad suggestion works without any flags.
func cmdWhy(args []string) error {
	fs := flag.NewFlagSet("hunch why", flag.ContinueOnError)
	stateStr := fs.String("state", "", "comma-separated prior commands (default: last 2 from zsh history)")
	cwd := fs.String("cwd", "", "working directory to explain (default: current directory)")
	priorOutcome := fs.String("prior-outcome", "", "outcome of the most recent command: success, failure, or empty")
	limit := fs.Int("limit", 5, "max candidates to show")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			*cwd = wd
		}
	}
	if *stateStr == "" {
		*stateStr = strings.Join(recentZshCommands(2), ",")
	}

	resp, err := explain(*stateStr, *cwd, *priorOutcome, "", *limit)
	if err != nil {
		return err
	}

	printExplainResult(resp)
	return nil
}

// recentZshCommands returns the last n commands from ~/.zsh_history, most
// recent last, or nil if the history file can't be read. Best-effort: a
// missing or unreadable history just means `hunch why` falls back to a
// stateless (CWD-only) query rather than failing outright.
func recentZshCommands(n int) []string {
	path, _, err := resolveHistoryPath("")
	if err != nil {
		return nil
	}
	cmds, err := parseZshHistory(path)
	if err != nil || len(cmds) == 0 {
		return nil
	}
	if len(cmds) > n {
		cmds = cmds[len(cmds)-n:]
	}
	return cmds
}

func printExplainResult(resp ipc.ExplainResponse) {
	fmt.Println("hunch why")
	fmt.Println()

	state := "(none)"
	if len(resp.State) > 0 {
		state = strings.Join(resp.State, " -> ")
	}
	fmt.Printf("context matched: %s\n", resp.Level)
	fmt.Printf("  cwd:   %s\n", displayOrNone(resp.CWD))
	fmt.Printf("  state: %s\n", state)
	fmt.Println()
	fmt.Printf("gates: a suggestion needs count >= %d always; a fallback context also needs score >= %.2f\n", resp.MinCount, resp.MinConfidence)
	fmt.Println()

	if len(resp.Breakdown) == 0 {
		fmt.Println("No candidates at this context - nothing has been observed here yet, or every candidate was gated out.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSCORE\tCOUNT\tDECAY\tCWD\tPRIOR\tACCEPT\tFAIL\tNEXT")
	for i, b := range resp.Breakdown {
		fmt.Fprintf(tw, "%d\t%.3f\t%d\t%.2f\t%s\t%s\t%s\t%s\t%s\n",
			i+1, b.Score, b.Count, b.DecayWeight,
			pct(b.CWDAffinity), pct(b.PriorAffinity), pct(b.AcceptRate), pct(b.FailureRate),
			b.Next,
		)
	}
	tw.Flush()

	fmt.Println()
	fmt.Println("DECAY is the time-recency weight (1.0 = just observed). CWD/PRIOR/ACCEPT/FAIL are")
	fmt.Println("the fraction of this candidate's observations that had that signal; '-' means the")
	fmt.Println("boost is off or no data exists, so it left the score unchanged.")
}

func pct(f float64) string {
	if f == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*f)
}

func displayOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
