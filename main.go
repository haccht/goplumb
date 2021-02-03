package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-isatty"
	"github.com/rivo/tview"
)

const bufSize = 65536

func getProgramName() string {
	return filepath.Base(os.Args[0])
}

type tui struct {
	*tview.Application
	layout *tview.Flex
	footer *tview.Flex

	MainView *tview.TextView
	SizeView *tview.TextView
	CmdInput *tview.InputField
}

func newTUI() *tui {
	ui := &tui{Application: tview.NewApplication()}

	ui.MainView = tview.NewTextView()
	ui.MainView.
		SetDynamicColors(true).
		SetBackgroundColor(tcell.Color235)

	ui.SizeView = tview.NewTextView()
	ui.SizeView.
		SetText(fmt.Sprint("0 bytes")).
		SetTextAlign(tview.AlignRight).
		SetTextColor(tcell.ColorDarkGray).
		SetBackgroundColor(tcell.ColorDefault)

	ui.CmdInput = tview.NewInputField()
	ui.CmdInput.
		SetLabel(fmt.Sprintf("%s | ", getProgramName())).
		SetLabelColor(tcell.ColorForestGreen).
		SetPlaceholder("cat").
		SetPlaceholderTextColor(tcell.ColorDarkGray).
		SetFieldBackgroundColor(tcell.ColorDefault).
		SetBackgroundColor(tcell.ColorDefault)

	ui.footer = tview.NewFlex()
	ui.footer.
		AddItem(ui.CmdInput, 0, 1, true).
		AddItem(ui.SizeView, 12, 0, false)

	ui.layout = tview.NewFlex().SetDirection(tview.FlexRow)
	ui.layout.
		AddItem(ui.MainView, 0, 1, false).
		AddItem(ui.footer, 1, 0, true)

	ui.SetRoot(ui.layout, true)
	return ui
}

func (ui *tui) GetInputText() string {
	text := strings.TrimSpace(ui.CmdInput.GetText())
	if text == "" {
		text = "cat"
	}
	return text
}

type history struct {
	pos   int
	Lines []string
}

func (h *history) Prev() string {
	if h.pos > 1 {
		h.pos--
	}
	return h.Lines[h.pos]
}

func (h *history) Next() string {
	if h.pos < len(h.Lines)-1 {
		h.pos++
	}
	return h.Lines[h.pos]
}

func (h *history) Append(line string) {
	h.pos = len(h.Lines)
	h.Lines = append(h.Lines, line)
}

type BufferedReader struct {
	buf *bytes.Buffer
	r   io.Reader
}

func NewBufferedReader(r io.Reader) *BufferedReader {
	buf := bytes.NewBuffer(nil)
	return &BufferedReader{
		buf: buf,
		r:   io.TeeReader(r, buf),
	}
}

func (br *BufferedReader) Rewind() {
	buf := bytes.NewBuffer(br.buf.Bytes())
	br.r = io.MultiReader(buf, br.r)
}

func (br *BufferedReader) Read(p []byte) (n int, err error) {
	return br.r.Read(p)
}

type App struct {
	ui *tui
	hi *history
	br *BufferedReader

	buf    *bytes.Buffer
	cancel context.CancelFunc
}

func NewApp() *App {
	a := &App{
		ui:  newTUI(),
		hi:  &history{},
		br:  NewBufferedReader(os.Stdin),
		buf: bytes.NewBuffer(nil),
	}

	a.ui.CmdInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			a.Stop()
			go a.Start()
		case tcell.KeyCtrlC:
			a.Stop()
			fmt.Printf("%s-- \n", a.buf.String())
			fmt.Printf("%s: %s\n", getProgramName(), a.ui.GetInputText())
		case tcell.KeyUp, tcell.KeyCtrlP:
			a.ui.CmdInput.SetText(a.hi.Prev())
		case tcell.KeyDown, tcell.KeyCtrlN:
			a.ui.CmdInput.SetText(a.hi.Next())
		case tcell.KeyCtrlD:
			return tcell.NewEventKey(tcell.KeyDelete, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlF:
			return tcell.NewEventKey(tcell.KeyRight, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlB:
			return tcell.NewEventKey(tcell.KeyLeft, event.Rune(), event.Modifiers())
		}
		return event
	})

	return a
}

func (a *App) Start() {
	r, w := io.Pipe()
	defer w.Close()

	a.buf.Reset()
	go func() {
		b := make([]byte, bufSize)
		t := tview.ANSIWriter(a.ui.MainView)

		for {
			n, err := r.Read(b)
			if n > 0 {
				str := tview.Escape(string(b[0:n]))
				t.Write([]byte(str))

				a.buf.Write(b[0:n])
				a.ui.SizeView.SetText(fmt.Sprintf("%6d bytes", a.buf.Len()))
				a.ui.Draw()
			}
			if err != nil {
				return
			}
		}
	}()

	ctx := context.Background()
	ctx, a.cancel = context.WithCancel(ctx)
	defer a.cancel()

	a.hi.Append(a.ui.GetInputText())
	a.br.Rewind()

	c := a.createCmd(ctx)
	c.Stdin = a.br
	c.Stdout = w
	c.Stderr = w

	c.Run()
}

func (a *App) Stop() {
	a.ui.MainView.Clear()
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *App) Run() error {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("stdin not found")
	}

	go a.Start()
	return a.ui.Run()
}

func (a *App) createCmd(ctx context.Context) *exec.Cmd {
	shell := os.Getenv("SHELL")
	if shell != "" {
		return exec.CommandContext(ctx, shell, "-c", a.ui.GetInputText())
	}

	shell, _ = exec.LookPath("sh")
	if shell != "" {
		return exec.CommandContext(ctx, shell, "-c", a.ui.GetInputText())
	}

	cmdArgs := strings.Fields(a.ui.GetInputText())
	return exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
}

func main() {
	app := NewApp()
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
