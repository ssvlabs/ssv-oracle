package logger

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is an alias for zap.SugaredLogger for use in type signatures.
type Logger = *zap.SugaredLogger

// L is the global logger instance.
var L *zap.SugaredLogger

func init() {
	L = zap.Must(zap.NewDevelopment()).Sugar()
}

// Init initializes the global logger.
// The level parameter sets the log level. Valid levels: debug, info, warn, error.
func Init(development bool, level string) {
	var config zap.Config
	if development {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05")
		config.DisableStacktrace = true
	} else {
		config = zap.NewProductionConfig()
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	// Set log level from parameter
	if level != "" {
		var l zapcore.Level
		if err := l.UnmarshalText([]byte(strings.ToLower(level))); err == nil {
			config.Level = zap.NewAtomicLevelAt(l)
		}
	}

	config.OutputPaths = []string{"stderr"}
	config.ErrorOutputPaths = []string{"stderr"}

	logger, err := config.Build(zap.AddCallerSkip(1))
	if err != nil {
		L = zap.NewNop().Sugar()
		return
	}

	L = logger.Sugar()
}

// InitFromEnv initializes the logger based on DEV env var.
// Used for early logging before config is loaded.
// Log level uses zap defaults; config file sets the final level.
func InitFromEnv() {
	dev := os.Getenv("DEV") == "true"
	Init(dev, "")
}

// Sync flushes any buffered log entries.
func Sync() {
	if L != nil {
		_ = L.Sync()
	}
}

// With returns a logger with additional context fields.
func With(keysAndValues ...any) *zap.SugaredLogger {
	return L.With(keysAndValues...)
}

func Debug(args ...any)                       { L.Debug(args...) }
func Debugf(template string, args ...any)     { L.Debugf(template, args...) }
func Debugw(msg string, keysAndValues ...any) { L.Debugw(msg, keysAndValues...) }

func Info(args ...any)                       { L.Info(args...) }
func Infof(template string, args ...any)     { L.Infof(template, args...) }
func Infow(msg string, keysAndValues ...any) { L.Infow(msg, keysAndValues...) }

func Warn(args ...any)                       { L.Warn(args...) }
func Warnf(template string, args ...any)     { L.Warnf(template, args...) }
func Warnw(msg string, keysAndValues ...any) { L.Warnw(msg, keysAndValues...) }

func Error(args ...any)                       { L.Error(args...) }
func Errorf(template string, args ...any)     { L.Errorf(template, args...) }
func Errorw(msg string, keysAndValues ...any) { L.Errorw(msg, keysAndValues...) }

func Fatal(args ...any)                       { L.Fatal(args...) }
func Fatalf(template string, args ...any)     { L.Fatalf(template, args...) }
func Fatalw(msg string, keysAndValues ...any) { L.Fatalw(msg, keysAndValues...) }
