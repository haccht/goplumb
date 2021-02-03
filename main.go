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
	"sync"

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

type bufferedReader struct {
	buf *bytes.Buffer
	tr  io.Reader
	mr  io.Reader
}

func newBufferedReader(r io.Reader) *bufferedReader {
	buf := bytes.NewBuffer(nil)
	return &bufferedReader{
		buf: buf,
		tr:  io.TeeReader(r, buf),
	}
}

func (br *bufferedReader) Rewind() {
	buf := bytes.NewBuffer(br.buf.Bytes())
	br.mr = io.MultiReader(buf, br.tr)
}

func (br *bufferedReader) Read(p []byte) (n int, err error) {
	return br.mr.Read(p)
}

type App struct {
	ui     *tui
	hi     *history
	br     *bufferedReader
	bu     *bytes.Buffer
	wc     io.WriteCloser
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

func NewApp(command string) *App {
	a := &App{
		ui: newTUI(),
		hi: &history{},
		br: newBufferedReader(os.Stdin),
		bu: bytes.NewBuffer(nil),
	}

	a.ui.CmdInput.SetText(command)
	a.ui.CmdInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEnter:
			a.Stop()
			a.Start()
		case tcell.KeyCtrlC:
			a.Stop()
			a.ui.Stop()
			fmt.Printf("%s-- \n", a.bu.String())
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
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	rc, wc := io.Pipe()
	a.wc = wc

	a.hi.Append(a.ui.GetInputText())
	a.bu.Reset()
	a.br.Rewind()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

		b := make([]byte, bufSize)
		t := tview.ANSIWriter(a.ui.MainView)

		for {
			n, err := rc.Read(b)
			if n > 0 {
				str := tview.Escape(string(b[0:n]))
				t.Write([]byte(str))

				a.bu.Write(b[0:n])
				a.ui.SizeView.SetText(fmt.Sprintf("%6d bytes", a.bu.Len()))
				a.ui.Draw()
			}
			if err != nil {
				return
			}
		}
	}()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()

		cmd := a.createCmd(ctx)
		cmd.Stdin = a.br
		cmd.Stdout = a.wc
		cmd.Stderr = a.wc

		cmd.Run()
	}()
}

func (a *App) Stop() {
	a.cancel()
	a.wc.Close()
	a.wg.Wait()

	a.ui.MainView.Clear()
}

func (a *App) Run() error {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("stdin not found")
	}

	a.Start()
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
	app := NewApp(strings.Join(os.Args[1:], " "))
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
