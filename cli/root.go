package cli

import (
	"fmt"
)

var Version = "dev"

func Run(args []string) error {
	if len(args) == 0 {
		return printUsage()
	}

	switch args[0] {
	case "--version", "-v":
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
  init <shell>       Print the source line for shell integration
  daemon <action>    Manage the daemon (run|start|stop|status)
  client <op>        Send an IPC operation (record|predict|reset|export)

Flags:
  --version, -v      Print version
  --help, -h         Print this help
`
}
