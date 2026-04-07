package app

import (
	"sqlgo/internal/db"
	"sqlgo/internal/ui"
)

type App struct {
	root *ui.Root
}

func New() (*App, error) {
	root := ui.NewRoot(db.DefaultRegistry())
	return &App{root: root}, nil
}

func (a *App) Run() error {
	return a.root.Run()
}
