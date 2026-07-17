package main

import (
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/tui"
)

const version = "0.0.1-dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("loyi " + version)
			return
		}
	}

	cfg, err := config.Load()
	if errors.Is(err, os.ErrNotExist) || (err == nil && !cfg.Onboarded) {
		if _, err := tea.NewProgram(tui.NewOnboarding()).Run(); err != nil {
			fmt.Fprintln(os.Stderr, "loyi:", err)
			os.Exit(1)
		}
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loyi: reading config:", err)
		os.Exit(1)
	}

	fmt.Printf("hi %s — the agent isn't here yet, but it's coming.\n", cfg.Name)
}
