package app

import (
	"github.com/nulifyer/sqlgo/internal/db"
	"github.com/nulifyer/sqlgo/internal/ui"
)

type App struct {
	root *ui.Root
}

func New() (*App, error) {
	root, err := ui.NewRoot(db.DefaultRegistry())
	if err != nil {
		return nil, err
	}
	return &App{root: root}, nil
}

func (a *App) Run() error {
	return a.root.Run()
}
