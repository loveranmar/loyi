package tui

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// pasteFromClipboard is a tea.Cmd that reads the system clipboard and replays
// it as a paste event, for terminals that pass ctrl+v through as a plain key
// instead of a bracketed paste.
func pasteFromClipboard() tea.Msg {
	text, err := readClipboard()
	if err != nil || text == "" {
		return nil
	}
	return tea.PasteMsg{Content: strings.TrimRight(text, "\r\n")}
}

func readClipboard() (string, error) {
	type tool struct {
		name string
		args []string
	}
	var tools []tool
	switch runtime.GOOS {
	case "darwin":
		tools = []tool{{"pbpaste", nil}}
	case "windows":
		tools = []tool{{"powershell", []string{"-NoProfile", "-Command", "Get-Clipboard"}}}
	default:
		tools = []tool{
			{"wl-paste", []string{"--no-newline"}},
			{"xclip", []string{"-out", "-selection", "clipboard"}},
			{"xsel", []string{"--output", "--clipboard"}},
		}
	}
	for _, t := range tools {
		if _, err := exec.LookPath(t.name); err != nil {
			continue
		}
		out, err := exec.Command(t.name, t.args...).Output()
		if err == nil {
			return string(out), nil
		}
	}
	return "", errors.New("no clipboard tool available")
}
