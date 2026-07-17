package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/mascot"
	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/provider/factory"
	"github.com/loveranmar/loyi/internal/theme"
	"github.com/loveranmar/loyi/internal/tool"
	"github.com/loveranmar/loyi/internal/tui"
)

const version = "0.0.1-dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			printVersion()
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

	runChat(cfg)
}

func printVersion() {
	th := theme.Default
	if cfg, err := config.Load(); err == nil {
		th = theme.Get(cfg.Theme)
	}
	s := th.Styles()
	fmt.Println()
	fmt.Println(mascot.Render(mascot.Full, mascot.Idle, th))
	fmt.Println()
	fmt.Println("  " + s.Accent.Render("loyi") + " " + s.Dim.Render(version))
	fmt.Println("  " + s.Dim.Render("your agentic cli, for people who actually ship."))
}

func runChat(cfg *config.Config) {
	if cfg.DefaultProvider == "" {
		fatal(fmt.Errorf("no provider configured — run `loyi setup`"))
	}
	ctx := context.Background()
	p, err := factory.Build(ctx, cfg, cfg.DefaultProvider)
	if err != nil {
		fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	ws, err := tool.NewWorkspace(cwd)
	if err != nil {
		fatal(err)
	}
	reg := tool.NewRegistry(
		&tool.ReadTool{WS: ws}, &tool.WriteTool{WS: ws}, &tool.EditTool{WS: ws},
		&tool.TreeTool{WS: ws}, &tool.LsTool{WS: ws}, &tool.GlobTool{WS: ws},
		&tool.GrepTool{WS: ws}, &tool.RunTool{WS: ws},
		&tool.WebSearchTool{}, &tool.WebFetchTool{},
	)
	sess := &agent.Session{
		Provider:  p,
		Tools:     reg,
		Agent:     agent.AgentByID(agent.DefaultAgentID),
		Workspace: ws.Root,
	}
	if _, err := tea.NewProgram(tui.NewChat(cfg, sess, theme.Get(cfg.Theme))).Run(); err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Println(`loyi — your agentic cli, for people who actually ship.

usage:
  loyi                 open the interactive coding agent (onboarding on first run)
  loyi ask [flags] "prompt"
                       one-shot: ask the configured model a question
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
