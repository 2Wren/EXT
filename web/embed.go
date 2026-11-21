package web

import (
	"embed"
	"io/fs"
)

//go:embed src/*
var files embed.FS

func Assets() fs.FS {
	dir, err := fs.Sub(files, "src")
	if err != nil {
		panic(err)
	}
	return dir
}
