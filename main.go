// Command iderm is a terminal PHP editor.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jannovy/iderm/internal/ui"
)

// version is set at build time by GoReleaser.
var version = "dev"

const usage = `Usage: iderm [directory] [file...]

Opens the given files; a directory becomes the project root shown in the
file tree (default: the current directory). Press F1 inside for help.
Set IDERM_PHP to choose the php binary used for syntax checks.
`

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var files []string
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			fmt.Print(usage)
			return
		}
		if arg == "-v" || arg == "--version" {
			fmt.Println("iderm", version)
			return
		}
		if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
			if root, err = filepath.Abs(arg); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			continue
		}
		abs, err := filepath.Abs(arg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		files = append(files, abs)
	}
	if err := ui.New(root, files).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
