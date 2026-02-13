// Package logging provides contextual logging helpers.
package logging

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"strings"
)

type Logger struct {
	base   *log.Logger
	fields map[string]any
}

func New(base *log.Logger, fields map[string]any) *Logger {
	if base == nil {
		base = log.Default()
	}
	return &Logger{
		base:   base,
		fields: cloneFields(fields),
	}
}

func (l *Logger) With(key string, value any) *Logger {
	fields := cloneFields(l.fields)
	if fields == nil {
		fields = map[string]any{}
	}
	fields[key] = value
	return &Logger{base: l.base, fields: fields}
}

func (l *Logger) WithFields(fields map[string]any) *Logger {
	if len(fields) == 0 {
		return l
	}
	merged := cloneFields(l.fields)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range fields {
		merged[key] = value
	}
	return &Logger{base: l.base, fields: merged}
}

func (l *Logger) Println(args ...any) {
	message := fmt.Sprintln(args...)
	l.base.Printf("%s%s", l.prefix(), message)
}

func (l *Logger) Printf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	l.base.Printf("%s%s", l.prefix(), message)
}

func (l *Logger) Fatalf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	l.base.Fatalf("%s%s", l.prefix(), message)
}

func CatchPanic(logger *Logger) {
	if r := recover(); r != nil {
		if logger == nil {
			logger = Default()
		}
		logger.Fatalf("panic: %v\nstack: %s", r, debug.Stack())
	}
}

func (l *Logger) prefix() string {
	if len(l.fields) == 0 {
		return ""
	}

	keys := make([]string, 0, len(l.fields))
	for key := range l.fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(fmt.Sprintf("%v", l.fields[key]))
		builder.WriteByte(' ')
	}
	return builder.String()
}

func cloneFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	copyFields := make(map[string]any, len(fields))
	for key, value := range fields {
		copyFields[key] = value
	}
	return copyFields
}

type contextKey struct{}

var defaultLogger = New(nil, nil)

func SetDefault(logger *Logger) {
	if logger == nil {
		return
	}
	defaultLogger = logger
}

func Default() *Logger {
	return defaultLogger
}

func WithContext(ctx context.Context, logger *Logger) context.Context {
	if logger == nil {
		logger = defaultLogger
	}
	return context.WithValue(ctx, contextKey{}, logger)
}

func FromContext(ctx context.Context) *Logger {
	if ctx == nil {
		return defaultLogger
	}
	if logger, ok := ctx.Value(contextKey{}).(*Logger); ok && logger != nil {
		return logger
	}
	return defaultLogger
}
