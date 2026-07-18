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

Commands:
  init [shell]         Set up shell integration (auto-detects shell from $SHELL)
    --auto             Automatically append source line to rc file
  daemon <action>      Manage the daemon (run|start|stop|status)
  client <op>          Send an IPC operation (record|predict|reset|export|normalize|stats|config|import|serve)
  import-history <sh>  Import shell history to jump-start predictions
  uninstall            Remove hunch from your system
    --yes, -y          Skip confirmation prompt
  doctor               Check hunch installation and daemon health
  update               Download and install the latest release
  version              Print version
  stats                Show daemon statistics (shortcut for: client stats)
  predict [flags]      Get top predictions (shortcut for: client predict)
    --state <s>        Comma-separated previous commands
    --prefix <p>       Filter by prefix
    --limit <n>        Max suggestions (default: 3)
  reset                Clear all learned transitions (shortcut for: client reset)

Flags:
  --version, -v      Print version
  --help, -h         Print this help
`
}
