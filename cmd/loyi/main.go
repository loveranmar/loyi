package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
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
		case "config":
			runConfig()
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
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	set, err := config.LoadSettings(cwd)
	if err != nil {
		fatal(err)
	}
	applySettings(cfg, set)
	if cfg.DefaultProvider == "" {
		fatal(fmt.Errorf("no provider configured — run `loyi setup`"))
	}
	ctx := context.Background()
	p, err := factory.Build(ctx, cfg, cfg.DefaultProvider)
	if err != nil {
		fatal(err)
	}
	ws, err := tool.NewWorkspace(cwd)
	if err != nil {
		fatal(err)
	}
	ws.Ignore = set.Context.Ignore
	ws.MaxFiles = set.Context.MaxFiles
	reg := tool.NewRegistry(
		&tool.ReadTool{WS: ws}, &tool.WriteTool{WS: ws}, &tool.EditTool{WS: ws},
		&tool.TreeTool{WS: ws}, &tool.LsTool{WS: ws}, &tool.GlobTool{WS: ws},
		&tool.GrepTool{WS: ws}, &tool.RunTool{WS: ws},
	)
	sess := &agent.Session{
		Provider:  p,
		Tools:     reg,
		Agent:     agent.AgentByID(agent.DefaultAgentID),
		Workspace: ws.Root,
		Model:     set.Model.Default,
		Effort:    provider.Effort(set.Model.Effort),
		Settings:  set,
	}
	if _, err := tea.NewProgram(tui.NewChat(cfg, set, sess, theme.Get(cfg.Theme))).Run(); err != nil {
		fatal(err)
	}
}

// applySettings folds loyi.json into the runtime config: theme, default
// provider, and API keys resolved from env references.
func applySettings(cfg *config.Config, set *config.Settings) {
	if set.Theme != "" {
		cfg.Theme = set.Theme
	}
	for id, ref := range set.Providers {
		if key := ref.Key(); key != "" {
			cfg.SetProvider(id, &config.Provider{Auth: "api_key", APIKey: key})
		}
	}
	if set.Model.Provider != "" {
		cfg.DefaultProvider = set.Model.Provider
	}
}

// runConfig prints the resolved settings: which files were read, the merged
// values, and whether provider env keys resolve. Never prints raw keys.
func runConfig() {
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	set, err := config.LoadSettings(cwd)
	if err != nil {
		fatal(err)
	}
	th := theme.Default
	if cfg, cerr := config.Load(); cerr == nil {
		th = theme.Get(cfg.Theme)
	}
	if set.Theme != "" {
		th = theme.Get(set.Theme)
	}
	s := th.Styles()

	global, project, hasProject := set.Sources()
	fmt.Println()
	fmt.Println("  " + s.Accent.Render("loyi") + s.Dim.Render(" · config"))
	fmt.Println()
	fmt.Println("  " + s.Dim.Render("global   ") + s.Text.Render(global))
	if hasProject {
		fmt.Println("  " + s.Dim.Render("project  ") + s.Text.Render(project) + s.Dim.Render("  (overrides global)"))
	} else {
		fmt.Println("  " + s.Dim.Render("project  ") + s.Dim.Render(project+"  (not found)"))
	}
	fmt.Println()

	b, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		fatal(err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		fmt.Println("  " + s.Text.Render(ln))
	}

	if len(set.Providers) > 0 {
		fmt.Println()
		ids := make([]string, 0, len(set.Providers))
		for id := range set.Providers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			ref := set.Providers[id]
			status := "not set"
			if ref.Key() != "" {
				status = "set"
			}
			fmt.Println("  " + s.Dim.Render(fmt.Sprintf("%s key (%s): %s", id, ref.APIKey, status)))
		}
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
  loyi config          print the resolved loyi.json settings
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
	if cwd, err := os.Getwd(); err == nil {
		if set, err := config.LoadSettings(cwd); err == nil {
			applySettings(cfg, set)
			if *model == "" {
				*model = set.Model.Default
			}
			if *effort == "" {
				*effort = set.Model.Effort
			}
		}
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
