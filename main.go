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
	ui      *tview.Application
	in      *inputBuffer
	result  *bytes.Buffer
	cancel  context.CancelFunc
	current int
	history []string

	Text *tview.TextView
	Size *tview.TextView
	Edit *tview.InputField
}

func NewApp(commandLine string) *App {
	a := &App{
		ui:      tview.NewApplication(),
		in:      NewInputBuffer(os.Stdin),
		result:  bytes.NewBufferString(""),
		cancel:  nil,
		current: 0,
		history: []string{},
	}

	a.Text = tview.NewTextView()
	a.Text.SetDynamicColors(true).
		SetBackgroundColor(tcell.Color235)

	a.Size = tview.NewTextView()
	a.Size.SetText(fmt.Sprintf("%6d bytes", a.result.Len())).
		SetTextAlign(tview.AlignRight).
		SetTextColor(tcell.ColorDarkGray).
		SetBackgroundColor(tcell.ColorDefault)

	a.Edit = tview.NewInputField()
	a.Edit.SetLabel(fmt.Sprintf("%s | ", getProgramName())).
		SetLabelColor(tcell.ColorForestGreen).
		SetPlaceholder("cat").
		SetPlaceholderTextColor(tcell.ColorDarkGray).
		SetFieldBackgroundColor(tcell.ColorDefault).
		SetBackgroundColor(tcell.ColorDefault)

	a.Edit.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			a.reset()
			a.runCommand(a.in.Reader())
		}
	})

	a.Edit.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			a.cancel()
			a.ui.Stop()
			fmt.Printf("%s-- \n%s: %s\n", a.result.String(), getProgramName(), a.getCommand())
		case tcell.KeyUp:
			if a.current > 0 {
				a.current--
				a.Edit.SetText(a.history[a.current])
				a.ui.Draw()
			}
		case tcell.KeyDown:
			if a.current < len(a.history)-1 {
				a.current++
				a.Edit.SetText(a.history[a.current])
				a.ui.Draw()
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
		a.Edit.SetText(commandLine)
	}

	footer := tview.NewFlex()
	footer.AddItem(a.Edit, 0, 1, true).
		AddItem(a.Size, 12, 0, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow)
	root.AddItem(a.Text, 0, 1, false).
		AddItem(footer, 1, 0, true)

	a.ui.SetRoot(root, true)
	return a
}

func (a *App) Run() error {
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("stdin not found")
	}

	a.runCommand(a.in.Reader())
	return a.ui.Run()
}

func (a *App) getCommand() string {
	commandLine := a.Edit.GetText()
	if commandLine == "" {
		commandLine = "cat"
	}

	return commandLine
}

func (a *App) runCommand(in io.Reader) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	a.current = len(a.history)
	a.history = append(a.history, a.getCommand())

	r := a.command(ctx, in)
	w := tview.ANSIWriter(a.Text)
	b := make([]byte, bufSize)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := r.Read(b)
				if n > 0 {
					es := tview.Escape(string(b[0:n]))
					w.Write([]byte(es))

					a.result.Write(b[0:n])
					a.Size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
					a.ui.Draw()
				}
				if err != nil {
					return
				}
			}
		}
	}()
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
		a.cancel()
		fmt.Fprintf(a.Text, "Error: %s", err)
		return r
	}

	go func() {
		c.Wait()
		w.Close()
	}()

	return r
}

func (a *App) reset() {
	a.cancel()
	a.result.Reset()
	a.Text.Clear()
	a.Size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
	a.ui.Draw()
}

type inputBuffer struct {
	r   io.Reader
	buf *bytes.Buffer
}

func NewInputBuffer(in io.Reader) *inputBuffer {
	b := bytes.NewBufferString("")
	r := io.TeeReader(in, b)
	return &inputBuffer{r: r, buf: b}
}

func (in *inputBuffer) Buffer() *bytes.Buffer {
	return bytes.NewBuffer(in.buf.Bytes())
}

func (in *inputBuffer) Reader() io.Reader {
	return io.MultiReader(in.Buffer(), in.r)
}

func main() {
	var args string
	if len(os.Args) > 0 {
		args = strings.Join(os.Args[1:], " ")
	}

	if err := NewApp(args).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
