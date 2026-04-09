package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up      key.Binding
	Down    key.Binding
	Enter   key.Binding
	Back    key.Binding
	Quit    key.Binding
	Help    key.Binding
	Create  key.Binding
	Delete  key.Binding
	Rotate  key.Binding
	Buckets key.Binding
	Users   key.Binding
	Access  key.Binding
	Tab     key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("up/k", "move up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("down/j", "move down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Create: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "create"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	Rotate: key.NewBinding(
		key.WithKeys("k"),
		key.WithHelp("k", "rotate key"),
	),
	Buckets: key.NewBinding(
		key.WithKeys("b"),
		key.WithHelp("b", "buckets"),
	),
	Users: key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "users"),
	),
	Access: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "access"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "next"),
	),
}
