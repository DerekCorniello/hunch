package cli

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/DerekCorniello/hunch/core/eval"
)

func cmdEval(args []string) error {
	var path string
	var warmup int

	fs := flag.NewFlagSet("hunch eval", flag.ContinueOnError)
	fs.StringVar(&path, "path", "", "history file path (overrides ~/.zsh_history)")
	fs.IntVar(&warmup, "warmup", eval.DefaultOptions().Warmup, "commands to learn from before scoring begins")
	if err := fs.Parse(args); err != nil {
		return err
	}

	historyPath, _, err := resolveHistoryPath(path)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Parsing zsh history...")
	rawCmds, err := parseZshHistory(historyPath)
	if err != nil {
		return fmt.Errorf("parse history: %w", err)
	}
	if len(rawCmds) == 0 {
		return fmt.Errorf("no commands found in %s", historyPath)
	}

	fmt.Fprintf(os.Stderr, "Normalizing %d commands...\n", len(rawCmds))
	templates, err := normalizeConcurrent(rawCmds, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("normalize: %w", err)
	}

	opts := eval.DefaultOptions()
	opts.Warmup = warmup
	if len(templates) <= opts.Warmup {
		return fmt.Errorf("history has %d commands, need more than the warmup of %d", len(templates), opts.Warmup)
	}

	commands := make([]eval.Command, len(templates))
	for i, tmpl := range templates {
		commands[i] = eval.Command{Template: tmpl}
	}

	printEvalResult(eval.Run(commands, opts), len(rawCmds))
	return nil
}

func printEvalResult(r eval.Result, historySize int) {
	fmt.Printf("\nhunch eval\n\n")
	fmt.Printf("history:   %d commands\n", historySize)
	fmt.Printf("scored:    %d (after warmup)\n", r.Scored)
	fmt.Printf("offered:   %d (%.1f%% of scored had any suggestion)\n", r.Offered, 100*r.Rate(r.Offered))
	fmt.Println()
	fmt.Printf("top-1:     %.1f%%\n", 100*r.Rate(r.Top1))
	fmt.Printf("top-3:     %.1f%%\n", 100*r.Rate(r.Top3))
	fmt.Printf("top-5:     %.1f%%\n", 100*r.Rate(r.Top5))
	fmt.Println()
	fmt.Printf("baseline:  %.1f%% (always guess your single most frequent command)\n", 100*r.Rate(r.BaselineTop1))

	lift := r.Rate(r.Top1) - r.Rate(r.BaselineTop1)
	fmt.Printf("lift:      %+.1f points over baseline\n", 100*lift)
	fmt.Println()
	fmt.Println("Each command is predicted using only the commands before it,")
	fmt.Println("which is how the daemon sees your history as you work.")
}
