package cli

import (
	"fmt"
	"io/fs"
)

var Version = "dev"

var IntegrationFS fs.FS

func Run(args []string) error {
	if len(args) == 0 {
		return printUsage()
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	case "--help", "-h":
		return printUsage()
	case "init":
		return cmdInit(args[1:])
	case "daemon":
		return cmdDaemon(args[1:])
	case "client":
		return cmdClient(args[1:])
	case "import-history":
		return cmdImportHistory(args[1:])
	case "eval":
		return cmdEval(args[1:])
	case "uninstall":
		skipConfirm := false
		for _, arg := range args[1:] {
			if arg == "--yes" || arg == "-y" {
				skipConfirm = true
			}
		}
		return cmdUninstall(skipConfirm)
	case "doctor":
		return cmdDoctor()
	case "update":
		return cmdUpdate()
	case "stats":
		return cmdClientStats()
	case "predict":
		return cmdClientPredict(args[1:])
	case "reset":
		return cmdClientReset()
	default:
		return fmt.Errorf("unknown command: %s\n\n%s", args[0], usageText())
	}
}

func printUsage() error {
	fmt.Print(usageText())
	return nil
}

func usageText() string {
	return `Usage: hunch <command> [options]

hunch learns your command history and suggests your next command as you type.
Set it up once with 'hunch init'; after that it runs in the background on its
own and you never need to touch it.

Commands:
  init [shell]         Install the shell integration
    --auto             Append the source line to your rc file for you
  doctor               Check installation and daemon health
  update               Update to the latest release
  uninstall [--yes]    Remove hunch and all its data
  import-history <sh>  Seed predictions from your existing shell history
  stats                Show what hunch has learned so far
  eval <shell>         Measure prediction accuracy against your own history
  reset                Forget everything and start over
  daemon <action>      Manage the background daemon (run|start|stop|status)
  version              Print the version

Flags:
  --version, -v        Print version
  --help, -h           Print this help
`
}
