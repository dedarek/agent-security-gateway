// Command asg-connect is the local probe installed on each developer machine.
// It is the "plugin" that connects any agent to the central ASG gateway:
//
//	asg-connect serve        — local LLM transparent proxy (/v1/*) + MCP shim
//	                           entry + event upload to the hub.
//	asg-connect check        — hook endpoint for Claude Code/Cursor hooks:
//	                           sync verdict on a tool call (bash/file/mcp).
//
// The agent keeps its own model choice and API key (configured in the probe
// config once); every byte of LLM traffic, every tool call and every command
// execution flows through the probe, gets a trace id, is enforced locally
// against the cached policy pack, and is reported to the central gateway.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		cfgPath := fs.String("config", "connect.yaml", "probe config path")
		dryRun := fs.Bool("dry-run", false, "validate config and exit without starting probe")
		fs.Parse(os.Args[2:])
		if *dryRun {
			if err := serveDryRun(*cfgPath); err != nil {
				fail(err)
			}
			return
		}
		if err := serve(*cfgPath); err != nil {
			fail(err)
		}
	case "check":
		// Hook protocol: read JSON {tool_name, tool_input} on stdin, print
		// permission decision on stdout, exit 0=allow 2=deny (Claude Code).
		if err := hookCheck(os.Args[2:]); err != nil {
			fail(err)
		}
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		app := fs.String("app", "universal", "universal (harness-agnostic, writes to ~/.config/asg/mcp.json; legacy claude-code|codex|cursor are deprecated)")
		fs.Parse(os.Args[2:])
		if err := initClient(*app); err != nil {
			fail(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `asg-connect — Agent Security Gateway local probe

Usage:
  asg-connect serve  [-config connect.yaml] [--dry-run]  run the local proxy + reporter
  asg-connect check  < hook-payload.json     sync verdict for an agent hook
  asg-connect init    [-app universal]  write generic ~/.config/asg/mcp.json to route via probe (harness-agnostic; per-harness apps deprecated)
`)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "asg-connect:", err)
	os.Exit(1)
}
