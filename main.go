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

const bufSize = 1024

func getName() string {
	return filepath.Base(os.Args[0])
}

type UI struct {
	*tview.Application
	layout *tview.Flex
	footer *tview.Flex

	MainView *tview.TextView
	SizeView *tview.TextView
	CmdInput *tview.InputField
}

func NewUI() *UI {
	ui := &UI{Application: tview.NewApplication()}

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
		SetLabel(fmt.Sprintf("%s | ", filepath.Base(os.Args[0]))).
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

func (ui *UI) GetInputText() string {
	text := strings.TrimSpace(ui.CmdInput.GetText())
	if text == "" {
		text = "cat"
	}
	return text
}

type History struct {
	pos   int
	Lines []string
}

func (h *History) Prev() string {
	if h.pos > 0 {
		h.pos--
	}
	return h.Lines[h.pos]
}

func (h *History) Next() string {
	if h.pos < len(h.Lines)-1 {
		h.pos++
	}
	return h.Lines[h.pos]
}

func (h *History) Append(line string) {
	h.Lines = append(h.Lines, line)
	h.pos = len(h.Lines)
}

type BufferedReader struct {
	buf *bytes.Buffer
	tr  io.Reader
	mr  io.Reader
}

func NewBufferedReader(r io.Reader) *BufferedReader {
	buf := bytes.NewBuffer(nil)
	tr := io.TeeReader(r, buf)
	return &BufferedReader{
		buf: buf,
		tr:  tr,
		mr:  tr,
	}
}

func (br *BufferedReader) Rewind() {
	buf := bytes.NewBuffer(br.buf.Bytes())
	br.mr = io.MultiReader(buf, br.tr)
}

func (br *BufferedReader) Read(p []byte) (n int, err error) {
	return br.mr.Read(p)
}

type App struct {
	ui *UI
	hi *History
	br *BufferedReader

	buf    *bytes.Buffer
	cancel context.CancelFunc
}

func NewApp() *App {
	a := &App{
		ui:  NewUI(),
		hi:  &History{},
		br:  NewBufferedReader(os.Stdin),
		buf: bytes.NewBuffer(nil),
	}

	a.ui.CmdInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			go a.Start()
		case tcell.KeyCtrlC:
			fmt.Printf("%s-- \n", a.buf.String())
			fmt.Printf("%s: %s\n", getName(), a.ui.GetInputText())
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

	if a.cancel != nil {
		a.cancel()
	}
	a.buf.Reset()
	a.ui.MainView.Clear()

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
