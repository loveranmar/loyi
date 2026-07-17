package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/provider/factory"
	"github.com/loveranmar/loyi/internal/tui"
)

const version = "0.0.1-dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Println("loyi " + version)
			return
		case "setup":
			runSetup()
			return
		case "ask":
			runAsk(args[1:])
			return
		case "help", "--help", "-h":
			usage()
			return
		}
	}

	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) || (err == nil && !cfg.Onboarded) {
		if _, err := tea.NewProgram(tui.NewOnboarding()).Run(); err != nil {
			fatal(err)
		}
		return
	}
	if err != nil {
		fatal(fmt.Errorf("reading config: %w", err))
	}

	fmt.Printf("hi %s — try `loyi ask \"...\"` or `loyi setup`.\n", cfg.Name)
}

func usage() {
	fmt.Println(`loyi — your agentic cli, for people who actually ship.

usage:
  loyi                 first run: onboarding. after that: status.
  loyi ask [flags] "prompt"
                       ask the configured model a question
      -p provider      provider to use (default: your default provider)
      -m model         model override
      -e effort        low | medium | high
  loyi setup           connect providers (logins, api keys)
  loyi version         print version`)
}

func runSetup() {
	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		cfg = &config.Config{}
	} else if err != nil {
		fatal(fmt.Errorf("reading config: %w", err))
	}
	if _, err := tea.NewProgram(tui.NewSetup(cfg)).Run(); err != nil {
		fatal(err)
	}
}

func runAsk(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	prov := fs.String("p", "", "provider id")
	model := fs.String("m", "", "model override")
	effort := fs.String("e", "", "effort: low | medium | high")
	_ = fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		fatal(fmt.Errorf("nothing to ask — usage: loyi ask \"prompt\""))
	}

	cfg, err := config.Load()
	if err != nil {
		fatal(fmt.Errorf("no config yet — run `loyi` to get set up"))
	}
	id := *prov
	if id == "" {
		id = cfg.DefaultProvider
	}
	if id == "" {
		fatal(fmt.Errorf("no provider configured — run `loyi setup`"))
	}

	ctx := context.Background()
	p, err := factory.Build(ctx, cfg, id)
	if err != nil {
		fatal(err)
	}

	req := provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: prompt}},
		Model:    *model,
		Effort:   provider.Effort(*effort),
	}
	ch, err := p.Stream(ctx, req)
	if err != nil {
		fatal(err)
	}
	for chunk := range ch {
		if chunk.Err != nil {
			fmt.Fprintln(os.Stderr)
			fatal(chunk.Err)
		}
		fmt.Print(chunk.Text)
	}
	fmt.Println()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "loyi:", err)
	os.Exit(1)
}
