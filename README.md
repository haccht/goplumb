# goplumb
A tool for writing pipes incrementally. Also support stream input like syslog.

![screencast](demo.gif)

## Usage
Writing pipes with files.
```
$ cat sample.txt | goplumb
```

Writing pipes with stream logs.
```
$ tail -f /path/to/log | goplumb
```

## Install
```
$ go get github.com/haccht/goplumb
```
