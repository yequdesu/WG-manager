package main

import (
	"flag"
	"fmt"
	"os"

	"wire-guard-dev/internal/cli"
)

type command struct {
	name        string
	description string
	run         func(*cli.Client, []string) error
}

func main() {
	configPath := flag.String("config", "config.env", "path to config.env")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [--config FILE] <command>\n\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output(), "Commands:")
		for _, cmd := range commands() {
			fmt.Fprintf(flag.CommandLine.Output(), "  %-8s %s\n", cmd.name, cmd.description)
		}
		fmt.Fprintln(flag.CommandLine.Output(), "\nFlags:")
		fmt.Fprintln(flag.CommandLine.Output(), "  --config FILE   config file path (default: config.env)")
	}

	args := os.Args[1:]
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			flag.Usage()
			return
		}
	}

	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}

	remaining := flag.Args()
	if len(remaining) == 0 {
		flag.Usage()
		return
	}

	commandName := remaining[0]
	clientConfig, apiKey, err := cli.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client := cli.NewClient(clientConfig, apiKey)
	for _, cmd := range commands() {
		if cmd.name == commandName {
			if err := cmd.run(client, remaining[1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", commandName)
	flag.Usage()
	os.Exit(1)
}

func commands() []command {
	return []command{
		{name: "peer", description: "peer operations", run: runPeer},
		{name: "invite", description: "invite operations", run: runInvite},
		{name: "user", description: "user operations", run: runUser},
		{name: "status", description: "status operations", run: runStatus},
		{name: "auth", description: "auth operations", run: runAuth},
		{name: "me", description: "current user operations", run: runMe},
	}
}

func runPeer(_ *cli.Client, _ []string) error {
	return printStub()
}

func runInvite(_ *cli.Client, _ []string) error {
	return printStub()
}

func runUser(_ *cli.Client, _ []string) error {
	return printStub()
}

func runStatus(_ *cli.Client, _ []string) error {
	return printStub()
}

func runAuth(_ *cli.Client, _ []string) error {
	return printStub()
}

func runMe(_ *cli.Client, _ []string) error {
	return printStub()
}

func printStub() error {
	fmt.Println("Subcommand not yet implemented")
	return nil
}
