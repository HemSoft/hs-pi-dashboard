// hs-pi-dashboard aggregates pi coding-agent sessions across a Tailscale
// fleet. One binary, two modes:
//
//	agent  – run on every machine with pi; watches ~/.pi/agent/sessions and
//	         serves a JSON snapshot for the dashboard.
//	serve  – run once on an always-on machine; polls every agent and serves
//	         the aggregated gold-on-black dashboard.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/HemSoft/hs-pi-dashboard/internal/agent"
	"github.com/HemSoft/hs-pi-dashboard/internal/fleet"
)

const version = "0.1.0"

// defaultFleet covers the five computers from the fleet map. The dashboard
// marks unreachable machines offline instead of failing.
const defaultFleet = "home|http://100.101.122.39:8787," +
	"laptop|http://100.117.202.124:8787," +
	"air|http://100.69.182.27:8787," +
	"mini|http://100.97.164.73:8787," +
	"grokbot|http://100.112.74.104:8787"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "agent":
		err = runAgent(os.Args[2:])
	case "serve":
		err = runServe(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("hs-pi-dashboard " + version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`hs-pi-dashboard ` + version + ` — aggregate pi sessions across the Tailscale fleet

Usage:
  hs-pi-dashboard agent  [flags]   watch local pi sessions, serve JSON
  hs-pi-dashboard serve  [flags]   poll agents, serve the dashboard
  hs-pi-dashboard version

Agent flags:
  -addr string          listen address (default ":8787"; bind the Tailscale IP
                        to keep the agent off the LAN, e.g. 100.101.122.39:8787)
  -dir string           pi sessions dir (default ~/.pi/agent/sessions)
  -machine string       label reported in the snapshot (default hostname)
  -active-window dur    how long a quiet session counts as active (default 2m)

Serve flags:
  -addr string          listen address (default ":8788"; bind the Tailscale IP
                        so phones can open it, e.g. 100.97.164.73:8788)
  -fleet spec           comma-separated name|url pairs of agents
  -poll dur             agent poll interval (default 5s)
`)
}

func runAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	addr := fs.String("addr", ":8787", "listen address")
	dir := fs.String("dir", "", "pi sessions dir (default ~/.pi/agent/sessions)")
	machine := fs.String("machine", "", "label reported in the snapshot (default hostname)")
	window := fs.Duration("active-window", 2*time.Minute, "how long a quiet session counts as active")
	if err := fs.Parse(args); err != nil {
		return err
	}
	label := strings.TrimSpace(*machine)
	if label == "" {
		host, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("resolve hostname: %w", err)
		}
		label = host
	}
	return agent.Run(agent.Options{
		Addr:         *addr,
		Dir:          *dir,
		Machine:      label,
		ActiveWindow: *window,
	})
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8788", "listen address")
	fleetSpec := fs.String("fleet", defaultFleet, "agents as comma-separated name|url pairs")
	poll := fs.Duration("poll", 5*time.Second, "agent poll interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targets, err := fleet.ParseTargets(*fleetSpec)
	if err != nil {
		return err
	}
	return fleet.NewServer(targets, *poll).Run(*addr)
}
