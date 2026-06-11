package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const modulePathSegment = "/smartinsure-eino-backend/"
const modulePathName = "smartinsure-eino-backend/"

type Options struct {
	FilePath  string
	ToConsole bool
}

// Configure routes all standard-library log output used by logx.
// A non-empty FilePath is opened in append mode; ToConsole keeps container
// stdout/stderr log collection available while also writing the file.
func Configure(opts Options) (func() error, error) {
	writers := make([]io.Writer, 0, 2)
	if opts.ToConsole {
		writers = append(writers, os.Stderr)
	}

	var file *os.File
	filePath := strings.TrimSpace(opts.FilePath)
	if filePath != "" {
		dir := filepath.Dir(filePath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		opened, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		file = opened
		writers = append(writers, opened)
	}

	if len(writers) == 0 {
		writers = append(writers, io.Discard)
	}
	log.SetOutput(io.MultiWriter(writers...))

	return func() error {
		if file == nil {
			return nil
		}
		return file.Close()
	}, nil
}

// Printf emits logs in the required bilingual, location-aware format:
// [file]==>(function) 中文说明 / English message key=value
func Printf(zh string, en string, format string, args ...any) {
	prefix := callerPrefix()
	message := bilingualMessage(zh, en)
	if strings.TrimSpace(format) != "" {
		message += " " + strings.TrimSpace(format)
	}
	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	log.Print(prefix + " " + message)
}

func callerPrefix() string {
	pc, file, _, ok := runtime.Caller(2)
	if !ok {
		return "[unknown]==>(unknown)"
	}
	if idx := strings.LastIndex(file, modulePathSegment); idx >= 0 {
		file = file[idx+len(modulePathSegment):]
	} else if idx := strings.LastIndex(file, modulePathName); idx >= 0 {
		file = file[idx+len(modulePathName):]
	}
	fn := "unknown"
	if runtimeFn := runtime.FuncForPC(pc); runtimeFn != nil {
		fn = shortFunctionName(runtimeFn.Name())
	}
	return fmt.Sprintf("[%s]==>(%s)", file, fn)
}

func bilingualMessage(zh string, en string) string {
	zh = strings.TrimSpace(zh)
	en = strings.TrimSpace(en)
	switch {
	case zh != "" && en != "":
		return zh + " / " + en
	case zh != "":
		return zh
	case en != "":
		return en
	default:
		return "运行日志 / runtime log"
	}
}

func shortFunctionName(name string) string {
	name = strings.TrimSpace(name)
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	if strings.HasPrefix(name, "smartinsure-eino-backend/") {
		name = strings.TrimPrefix(name, "smartinsure-eino-backend/")
	}
	return name
}
