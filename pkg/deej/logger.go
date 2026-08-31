package deej

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/omriharel/deej/pkg/deej/util"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	buildTypeNone    = ""
	buildTypeDev     = "dev"
	buildTypeRelease = "release"

	logDirectory     = "logs"
	logRetentionDays = 30
)

// dailyFileWriter rotates log files on a daily basis (YYYY-MM-DD.log) and cleans up old logs
type dailyFileWriter struct {
	dir           string
	retentionDays int
	currentDay    string
	file          *os.File
	lock          sync.Mutex
	nowFunc       func() time.Time // allows mocking time in tests
}

func newDailyFileWriter(dir string, retentionDays int) (*dailyFileWriter, error) {
	if err := util.EnsureDirExists(dir); err != nil {
		return nil, fmt.Errorf("ensure log directory exists: %w", err)
	}

	w := &dailyFileWriter{
		dir:           dir,
		retentionDays: retentionDays,
		nowFunc:       time.Now,
	}

	if err := w.rotateIfNeeded(); err != nil {
		return nil, err
	}

	return w, nil
}

func (w *dailyFileWriter) rotateIfNeeded() error {
	today := w.nowFunc().Format("2006-01-02")
	if w.file != nil && w.currentDay == today {
		return nil
	}

	if w.file != nil {
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}

	filename := filepath.Join(w.dir, today+".log")
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", filename, err)
	}

	w.file = f
	w.currentDay = today

	// Trigger cleanup of logs older than retention period
	go w.cleanOldLogs()

	return nil
}

func (w *dailyFileWriter) Write(p []byte) (n int, err error) {
	w.lock.Lock()
	defer w.lock.Unlock()

	if err := w.rotateIfNeeded(); err != nil {
		return 0, err
	}

	return w.file.Write(p)
}

func (w *dailyFileWriter) Sync() error {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

func (w *dailyFileWriter) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

func (w *dailyFileWriter) cleanOldLogs() {
	if w.retentionDays <= 0 {
		return
	}

	dirFile, err := os.Open(w.dir)
	if err != nil {
		return
	}
	defer dirFile.Close()

	fileInfos, err := dirFile.Readdir(-1)
	if err != nil {
		return
	}

	cutoff := w.nowFunc().AddDate(0, 0, -w.retentionDays)

	for _, info := range fileInfos {
		if info.IsDir() {
			continue
		}

		name := info.Name()
		// Matches YYYY-MM-DD.log format (length 14: 10 chars date + 4 chars .log)
		if len(name) == 14 && filepath.Ext(name) == ".log" {
			dateStr := name[:10]
			if logDate, err := time.Parse("2006-01-02", dateStr); err == nil {
				if logDate.Before(cutoff) {
					_ = os.Remove(filepath.Join(w.dir, name))
				}
			}
		}
	}
}

// NewLogger provides a logger instance for the whole program
func NewLogger(buildType string) (*zap.SugaredLogger, error) {
	encoderConfig := zapcore.EncoderConfig{
		MessageKey:    "msg",
		LevelKey:      "level",
		TimeKey:       "ts",
		NameKey:       "logger",
		CallerKey:     "",
		StacktraceKey: "stacktrace",
		LineEnding:    zapcore.DefaultLineEnding,
		EncodeLevel:   zapcore.CapitalLevelEncoder,
		EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
		},
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   nil,
		EncodeName: func(s string, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(fmt.Sprintf("%-27s", s))
		},
	}

	// Development mode: debug level to stderr with colors
	if buildType == buildTypeDev {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		core := zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.Lock(os.Stderr),
			zap.DebugLevel,
		)
		logger := zap.New(core)
		return logger.Sugar(), nil
	}

	// Release / default mode: daily rotating file in logs/YYYY-MM-DD.log
	fileWriter, err := newDailyFileWriter(logDirectory, logRetentionDays)
	if err != nil {
		return nil, fmt.Errorf("create daily file writer: %w", err)
	}

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(fileWriter),
		zap.DebugLevel,
	)

	logger := zap.New(core)
	return logger.Sugar(), nil
}
