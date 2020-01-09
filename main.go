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
	in     *inputBuffer
	result *bytes.Buffer

	pos     int
	history []string

	cmdDone chan struct{}
	cmdStop context.CancelFunc

	Text *tview.TextView
	Size *tview.TextView
	Edit *tview.InputField
}

func NewApp(commandLine string) *App {
	a := &App{
		ui:     tview.NewApplication(),
		in:     NewInputBuffer(os.Stdin),
		result: bytes.NewBuffer(nil),
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
	a.Edit.SetLabel(fmt.Sprintf("%s | ", getName())).
		SetLabelColor(tcell.ColorForestGreen).
		SetPlaceholder("cat").
		SetPlaceholderTextColor(tcell.ColorDarkGray).
		SetFieldBackgroundColor(tcell.ColorDefault).
		SetBackgroundColor(tcell.ColorDefault)

	a.Edit.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			a.cmdStop()
			<-a.cmdDone

			a.result.Reset()
			a.Size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
			a.Text.Clear()
			go a.runCmd()
		}
	})

	a.Edit.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlC:
			a.cmdStop()
			<-a.cmdDone

			a.ui.Stop()
			fmt.Printf("%s", a.result.String())
			fmt.Printf("-- \n%s: %s\n", getName(), a.getCmd())
		case tcell.KeyUp:
			if a.pos > 0 {
				a.pos--
				a.Edit.SetText(a.history[a.pos])
			}
		case tcell.KeyDown:
			if a.pos < len(a.history)-1 {
				a.pos++
				a.Edit.SetText(a.history[a.pos])
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
	_, err := getShell()
	if err != nil {
		return err
	}

	go a.runCmd()
	return a.ui.Run()
}

func (a *App) getCmd() string {
	commandLine := a.Edit.GetText()
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

	a.cmdDone = make(chan struct{})
	defer close(a.cmdDone)

	go func() {
		buf := make([]byte, bufSize)
		txt := tview.ANSIWriter(a.Text)

		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := r.Read(buf)
				if n > 0 {
					str := tview.Escape(string(buf[0:n]))
					txt.Write([]byte(str))

					a.result.Write(buf[0:n])
					a.Size.SetText(fmt.Sprintf("%6d bytes", a.result.Len()))
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

	err := c.Start()
	if err != nil {
		fmt.Fprintf(a.Text, "Error: %s", err)
		return
	}

	a.pos = len(a.history)
	a.history = append(a.history, a.getCmd())

	c.Wait()
}

type inputBuffer struct {
	r   io.Reader
	buf *bytes.Buffer
}

func NewInputBuffer(in io.Reader) *inputBuffer {
	b := bytes.NewBuffer(nil)
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

	app := NewApp(args)
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
