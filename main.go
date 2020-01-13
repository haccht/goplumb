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

	"github.com/gdamore/tcell"
	"github.com/mattn/go-isatty"
	"github.com/rivo/tview"
)

const bufSize = 32 * 1024

func getName() string {
	return filepath.Base(os.Args[0])
}

func getShell() (string, error) {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return "", fmt.Errorf("stdin not found")
	}

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
	in     *InputBuffer
	result *bytes.Buffer

	pos     int
	history []string

	cmdDone chan struct{}
	cmdStop context.CancelFunc

	text *tview.TextView
	size *tview.TextView
	edit *tview.InputField
}

func NewApp(commandLine string) *App {
	a := &App{
		ui:     tview.NewApplication(),
		in:     NewInputBuffer(os.Stdin),
		result: bytes.NewBuffer(nil),
	}

	a.text = tview.NewTextView()
	a.text.SetDynamicColors(true).
		SetBackgroundColor(tcell.Color235)

	a.size = tview.NewTextView()
	a.size.SetText(fmt.Sprintf("%6d bytes", a.result.Len())).
		SetTextAlign(tview.AlignRight).
		SetTextColor(tcell.ColorDarkGray).
		SetBackgroundColor(tcell.ColorDefault)

	a.edit = tview.NewInputField()
	a.edit.SetLabel(fmt.Sprintf("%s | ", getName())).
		SetLabelColor(tcell.ColorForestGreen).
		SetPlaceholder("cat").
		SetPlaceholderTextColor(tcell.ColorDarkGray).
		SetFieldBackgroundColor(tcell.ColorDefault).
		SetBackgroundColor(tcell.ColorDefault)

	a.edit.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			a.cmdStop()
			<-a.cmdDone

			a.result.Reset()
			a.size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
			a.text.Clear()
			go a.runCmd()
		}
	})

	a.edit.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			a.cmdStop()
			<-a.cmdDone

			a.ui.Stop()
			fmt.Printf("%s-- \n", a.result.String())
			fmt.Printf("%s: %s\n", getName(), a.getCmd())
		case tcell.KeyUp, tcell.KeyCtrlP:
			if a.pos > 0 {
				a.pos--
				a.edit.SetText(a.history[a.pos])
			}
		case tcell.KeyDown, tcell.KeyCtrlN:
			if a.pos < len(a.history)-1 {
				a.pos++
				a.edit.SetText(a.history[a.pos])
			}
		case tcell.KeyCtrlD:
			return tcell.NewEventKey(tcell.KeyDelete, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlF:
			return tcell.NewEventKey(tcell.KeyRight, event.Rune(), event.Modifiers())
		case tcell.KeyCtrlB:
			return tcell.NewEventKey(tcell.KeyLeft, event.Rune(), event.Modifiers())
		}
		return event
	})

	if commandLine != "" {
		a.edit.SetText(commandLine)
	}

	footer := tview.NewFlex()
	footer.AddItem(a.edit, 0, 1, true).
		AddItem(a.size, 12, 0, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(a.text, 0, 1, false).
		AddItem(footer, 1, 0, true)

	a.ui.SetRoot(root, true)
	return a
}

func (a *App) Run() error {
	_, err := getShell()
	if err != nil {
		return err
	}

	go a.runCmd()
	return a.ui.Run()
}

func (a *App) getCmd() string {
	commandLine := strings.TrimSpace(a.edit.GetText())
	if commandLine == "" {
		commandLine = "cat"
	}

	return commandLine
}

func (a *App) runCmd() {
	r, w := io.Pipe()
	defer w.Close()

	ctx := context.Background()
	ctx, a.cmdStop = context.WithCancel(ctx)
	defer a.cmdStop()

	// ensure to stop subprocess properly
	a.cmdDone = make(chan struct{})
	defer close(a.cmdDone)

	go func() {
		b := make([]byte, bufSize)
		t := tview.ANSIWriter(a.text)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := r.Read(b)
				if n > 0 {
					str := tview.Escape(string(b[0:n]))
					t.Write([]byte(str))

					a.result.Write(b[0:n])
					a.size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
					a.ui.Draw()
				}
				if err != nil {
					return
				}
			}
		}
	}()

	shell, _ := getShell()
	c := exec.CommandContext(ctx, shell, "-c", a.getCmd())
	c.Stdin = a.in.Reader()
	c.Stdout = w
	c.Stderr = w

	a.pos = len(a.history)
	a.history = append(a.history, a.getCmd())

	c.Run()
}

type InputBuffer struct {
	r   io.Reader
	buf *bytes.Buffer
}

func NewInputBuffer(in io.Reader) *InputBuffer {
	b := bytes.NewBuffer(nil)
	r := io.TeeReader(in, b)
	return &InputBuffer{r: r, buf: b}
}

func (in *InputBuffer) Buffer() *bytes.Buffer {
	return bytes.NewBuffer(in.buf.Bytes())
}

func (in *InputBuffer) Reader() io.Reader {
	return io.MultiReader(in.Buffer(), in.r)
}

func main() {
	var args string
	if len(os.Args) > 0 {
		args = strings.Join(os.Args[1:], " ")
	}

	app := NewApp(args)
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
