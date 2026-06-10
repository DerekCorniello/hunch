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
	cli.IntegrationFS, _ = fs.Sub(embeddedIntegrations, "integrations")
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
