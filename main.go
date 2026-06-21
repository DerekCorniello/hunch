package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/DerekCorniello/hunch/cli"
)

//go:embed integrations/zsh/hunch.zsh integrations/bash/hunch.bash integrations/fish/hunch.fish integrations/powershell/hunch.ps1
var embeddedIntegrations embed.FS

func main() {
	sub, err := fs.Sub(embeddedIntegrations, "integrations")
	if err != nil {
		fmt.Fprintf(os.Stderr, "embedded integrations: %v\n", err)
		os.Exit(1)
	}
	cli.IntegrationFS = sub
	if err := cli.Run(os.Args[1:]); err != nil {
		if err != cli.ErrDaemonNotRunning {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
