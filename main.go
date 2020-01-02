package main

import (
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

const bufSize = 32 * 1024

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

func getProgramName() string {
	return filepath.Base(os.Args[0])
}

type App struct {
	ui     *tview.Application
	br     *bufferedReader
	result *bytes.Buffer
	cancel context.CancelFunc

	Edit *tview.InputField
	Text *tview.TextView
	Size *tview.TextView
}

func NewApp() *App {
	a := &App{
		ui:     tview.NewApplication(),
		br:     NewBufferedReader(os.Stdin),
		result: bytes.NewBufferString(""),
		cancel: nil,
	}

	a.Edit = tview.NewInputField()
	a.Edit.SetLabel(fmt.Sprintf("%s | ", getProgramName())).SetLabelColor(tcell.ColorForestGreen)
	a.Edit.SetPlaceholder("cat").SetPlaceholderTextColor(tcell.ColorDarkGray)
	a.Edit.SetBackgroundColor(tcell.ColorDefault)
	a.Edit.SetFieldBackgroundColor(tcell.ColorDefault)
	a.Edit.SetDoneFunc(func(key tcell.Key) {
		a.cancel()
		a.Text.Clear()
		a.Size.Clear()

		a.br.mu.Lock()
		a.result.Reset()
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
			fmt.Printf("%s--\n%s: %s\n", a.result.String(), getProgramName(), a.getCommand())
		case tcell.KeyCtrlD:
			return tcell.NewEventKey(tcell.KeyDelete, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlF:
			return tcell.NewEventKey(tcell.KeyRight, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlB:
			return tcell.NewEventKey(tcell.KeyLeft, event.Rune(), event.Modifiers())
		}

		return event
	})

	a.Size = tview.NewTextView()
	a.Size.SetTextAlign(tview.AlignRight).SetTextColor(tcell.ColorDarkGray)
	a.Size.SetBackgroundColor(tcell.ColorDefault)

	a.Text = tview.NewTextView()
	a.Text.SetDynamicColors(true)
	a.Text.SetBackgroundColor(tcell.Color235)

	footer := tview.NewFlex()
	footer.AddItem(a.Edit, 0, 1, true).AddItem(a.Size, 12, 0, false)

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
		a.render(r)
	} else {
		go a.render(r)
	}
}

func (a *App) render(r io.Reader) {
	w := tview.ANSIWriter(a.Text)
	b := make([]byte, bufSize)

	for {
		n, err := r.Read(b)
		if n > 0 {
			es := tview.Escape(string(b[0:n]))
			w.Write([]byte(es))

			a.result.Write(b[0:n])
			a.Size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
			a.ui.Draw()
		}
		if err == io.EOF {
			break
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
	if err := NewApp().Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
