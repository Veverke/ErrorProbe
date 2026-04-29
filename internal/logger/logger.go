package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	mu         sync.Mutex
	fileLogger *log.Logger
	roller     *lumberjack.Logger
	debugMode  bool
)

const timeLayout = "2006-01-02T15:04:05Z07:00"

// Init initialises the logger, writing to path (rotated) and echoing Info/Error to stdout.
func Init(path string, maxSizeMB int, maxBackups int) error {
	mu.Lock()
	defer mu.Unlock()

	if roller != nil {
		_ = roller.Close()
		roller = nil
	}
	fileLogger = nil

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}

	r := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
	}

	fileLogger = log.New(r, "", 0)
	roller = r
	return nil
}

// SetDebug enables or disables debug-level output.
func SetDebug(enabled bool) {
	mu.Lock()
	defer mu.Unlock()
	debugMode = enabled
}

// Close releases the underlying log file. Primarily used in tests.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if roller != nil {
		_ = roller.Close()
		roller = nil
	}
	fileLogger = nil
}

// Info logs a message at INFO level to file and stderr.
func Info(msg string, fields ...any) {
	write(os.Stderr, "INFO", msg, fields...)
}

// Error logs a message at ERROR level to file and stdout.
func Error(msg string, fields ...any) {
	write(os.Stderr, "ERROR", msg, fields...)
}

// Debug logs a message at DEBUG level to file only; suppressed unless debug mode is on.
func Debug(msg string, fields ...any) {
	mu.Lock()
	enabled := debugMode
	mu.Unlock()
	if !enabled {
		return
	}
	writeFile("DEBUG", msg, fields...)
}

func write(out io.Writer, level, msg string, fields ...any) {
	line := formatLine(level, msg, fields...)
	writeToFile(line)
	fmt.Fprintln(out, line)
}

func writeFile(level, msg string, fields ...any) {
	line := formatLine(level, msg, fields...)
	writeToFile(line)
}

func writeToFile(line string) {
	mu.Lock()
	defer mu.Unlock()
	if fileLogger != nil {
		fileLogger.Println(line)
	}
}

func formatLine(level, msg string, fields ...any) string {
	ts := time.Now().Format(timeLayout)
	if len(fields) == 0 {
		return fmt.Sprintf("%s %s %s", ts, level, msg)
	}
	pairs := make([]string, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		pairs = append(pairs, fmt.Sprintf("%v=%v", fields[i], fields[i+1]))
	}
	if len(fields)%2 != 0 {
		pairs = append(pairs, fmt.Sprintf("%v", fields[len(fields)-1]))
	}
	return fmt.Sprintf("%s %s %s [%s]", ts, level, msg, strings.Join(pairs, " "))
}