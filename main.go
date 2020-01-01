package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/gdamore/tcell"
	"github.com/mattn/go-isatty"
	"github.com/rivo/tview"
)

func getShell() (string, error) {
	shell := os.Getenv("SHELL")
	if shell != "" {
		return shell, nil
	}

	shell, _ = exec.LookPath("bash")
	if shell != "" {
		return shell, nil
	}

	shell, _ = exec.LookPath("sh")
	if shell != "" {
		return shell, nil
	}

	return "", fmt.Errorf("shell not found")
}

type App struct {
	ui     *tview.Application
	br     *bufferedReader
	result []string
	cancel context.CancelFunc

	Edit  *tview.InputField
	Text  *tview.TextView
	Count *tview.TextView
}

func NewApp() *App {
	a := &App{
		ui:     tview.NewApplication(),
		br:     NewBufferedReader(os.Stdin),
		result: []string{},
	}

	a.Edit = tview.NewInputField()
	a.Edit.SetLabel("-->| ").SetLabelColor(tcell.ColorLawnGreen)
	a.Edit.SetPlaceholder("cat").SetPlaceholderTextColor(tcell.ColorDarkGray)
	a.Edit.SetFieldBackgroundColor(tcell.ColorDefault)
	a.Edit.SetDoneFunc(func(key tcell.Key) {
		a.cancel()
		a.Text.Clear()
		a.Count.Clear()

		a.br.mu.Lock()
		a.result = nil
		a.runCommand(a.br.Buffer(), true)
		a.br.mu.Unlock()

		if !a.br.eof {
			a.runCommand(a.br, false)
		}
	})
	a.Edit.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			a.ui.Stop()
			for _, line := range a.result {
				fmt.Println(line)
			}
			fmt.Printf("\n%s: %s\n", filepath.Base(os.Args[0]), a.getCommand())
		case tcell.KeyCtrlD:
			return tcell.NewEventKey(tcell.KeyDelete, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlF:
			return tcell.NewEventKey(tcell.KeyRight, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlB:
			return tcell.NewEventKey(tcell.KeyLeft, event.Rune(), event.Modifiers())
		}

		return event
	})

	a.Count = tview.NewTextView()
	a.Count.SetTextAlign(tview.AlignRight).SetTextColor(tcell.ColorDarkGray)
	a.Count.SetBackgroundColor(tcell.ColorDefault)

	a.Text = tview.NewTextView()
	a.Text.SetDynamicColors(true)

	footer := tview.NewFlex()
	footer.AddItem(a.Edit, 0, 1, true).AddItem(a.Count, 10, 0, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(a.Text, 0, 1, false).AddItem(footer, 1, 0, true)

	a.ui.SetRoot(root, true)
	return a
}

func (a *App) getCommand() string {
	commandLine := a.Edit.GetText()
	if commandLine == "" {
		commandLine = "cat"
	}

	return commandLine
}

func (a *App) runCommand(in io.Reader, synchronize bool) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	r := a.command(ctx, in)
	if synchronize {
		a.render(ctx, r)
	} else {
		go a.render(ctx, r)
	}
}

func (a *App) render(ctx context.Context, r io.Reader) {
	s := bufio.NewScanner(r)
	for s.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			fmt.Fprintln(a.Text, tview.TranslateANSI(s.Text()))

			a.result = append(a.result, s.Text())
			a.Count.SetText(fmt.Sprintf("%d lines", len(a.result)))
		}
	}
}

func (a *App) command(ctx context.Context, in io.Reader) io.Reader {
	r, w := io.Pipe()

	shell, err := getShell()
	if err != nil {
		fmt.Fprintf(a.Text, "Error: %s", err)
		return r
	}

	c := exec.CommandContext(ctx, shell, "-c", a.getCommand())
	c.Stdin = in
	c.Stdout = w
	c.Stderr = w

	err = c.Start()
	if err != nil {
		fmt.Fprintf(a.Text, "Error: %s", err)
		return r
	}

	go func() {
		c.Wait()
		w.Close()
	}()

	return r
}

func (a *App) Run() error {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("stdin not found")
	}

	a.runCommand(a.br, false)
	return a.ui.Run()
}

type bufferedReader struct {
	mu  sync.Mutex
	in  io.Reader
	buf *bytes.Buffer
	eof bool
}

func NewBufferedReader(in io.Reader) *bufferedReader {
	b := bytes.NewBufferString("")
	r := io.TeeReader(in, b)
	return &bufferedReader{
		mu:  sync.Mutex{},
		in:  r,
		buf: b,
		eof: false,
	}
}

func (br *bufferedReader) Buffer() *bytes.Buffer {
	return bytes.NewBuffer(br.buf.Bytes())
}

func (br *bufferedReader) Read(p []byte) (n int, err error) {
	br.mu.Lock()
	defer br.mu.Unlock()

	n, err = br.in.Read(p)
	if err == io.EOF {
		br.eof = true
	}
	return
}

func main() {
	a := NewApp()
	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
