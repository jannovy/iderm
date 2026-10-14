# iderm

A terminal PHP IDE written in Go – a single static binary for macOS and Linux.

- **Editor** – PHP syntax highlighting, undo/redo, auto-indent, find/replace, `php -l` syntax check while you type
- **Code navigation** – project-wide index of classes, methods, properties and functions (including `vendor/`);
  double-click, F12 or Ctrl+] jumps to the definition, Ctrl+T goes back
- **Search everywhere** (Ctrl+P) – file names, classes and full text
- **Git** – change markers in the gutter, status colors, stage/commit/diff/push/pull, branches and a commit history graph
- **File manager** – two panels with Midnight Commander keys (F5 copy, F6 move, F7 mkdir, F8 delete …)

Switch between the **IDE | Files | Git** modes in the top bar. Press F1 for all shortcuts.

## Install

```sh
brew install --cask jannovy/tap/iderm
```

or download a binary from [Releases](https://github.com/jannovy/iderm/releases), or

```sh
go install github.com/jannovy/iderm@latest
```

PHP (`php-cli`) and `git` are optional: without them the syntax check or the git features are disabled.

## Usage

```sh
iderm                    # current directory as the project
iderm ~/project src/A.php
```
