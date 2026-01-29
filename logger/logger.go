package logger

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is an alias for zap.SugaredLogger for use in type signatures.
type Logger = *zap.SugaredLogger

var l *zap.SugaredLogger

func init() {
	l = zap.Must(zap.NewDevelopment()).Sugar()
}

// Init initializes the global logger.
// Development mode is determined by DEV=true env var.
// The level parameter sets the log level. Valid levels: debug, info, warn, error.
// Panics if logger configuration fails (logging is critical infrastructure).
func Init(level string) {
	// Sync old logger before replacing to avoid losing buffered logs
	if l != nil {
		_ = l.Sync()
	}

	dev := os.Getenv("DEV") == "true"

	var config zap.Config
	if dev {
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
		var lvl zapcore.Level
		if err := lvl.UnmarshalText([]byte(strings.ToLower(level))); err == nil {
			config.Level = zap.NewAtomicLevelAt(lvl)
		}
	}

	config.OutputPaths = []string{"stderr"}
	config.ErrorOutputPaths = []string{"stderr"}

	logger, err := config.Build(zap.AddCallerSkip(1))
	if err != nil {
		panic("failed to build logger: " + err.Error())
	}

	l = logger.Sugar()
}

// Sync flushes any buffered log entries.
func Sync() {
	if l != nil {
		_ = l.Sync()
	}
}

// With returns a logger with additional context fields.
func With(keysAndValues ...any) *zap.SugaredLogger {
	return l.With(keysAndValues...)
}

// Debug logs at Debug level.
func Debug(args ...any) { l.Debug(args...) }

// Debugf logs a formatted message at Debug level.
func Debugf(template string, args ...any) { l.Debugf(template, args...) }

// Debugw logs a message with key-value pairs at Debug level.
func Debugw(msg string, keysAndValues ...any) { l.Debugw(msg, keysAndValues...) }

// Info logs at Info level.
func Info(args ...any) { l.Info(args...) }

// Infof logs a formatted message at Info level.
func Infof(template string, args ...any) { l.Infof(template, args...) }

// Infow logs a message with key-value pairs at Info level.
func Infow(msg string, keysAndValues ...any) { l.Infow(msg, keysAndValues...) }

// Warn logs at Warn level.
func Warn(args ...any) { l.Warn(args...) }

// Warnf logs a formatted message at Warn level.
func Warnf(template string, args ...any) { l.Warnf(template, args...) }

// Warnw logs a message with key-value pairs at Warn level.
func Warnw(msg string, keysAndValues ...any) { l.Warnw(msg, keysAndValues...) }

// Error logs at Error level.
func Error(args ...any) { l.Error(args...) }

// Errorf logs a formatted message at Error level.
func Errorf(template string, args ...any) { l.Errorf(template, args...) }

// Errorw logs a message with key-value pairs at Error level.
func Errorw(msg string, keysAndValues ...any) { l.Errorw(msg, keysAndValues...) }

// Fatal logs at Fatal level and then exits.
func Fatal(args ...any) { l.Fatal(args...) }

// Fatalf logs a formatted message at Fatal level and then exits.
func Fatalf(template string, args ...any) { l.Fatalf(template, args...) }

// Fatalw logs a message with key-value pairs at Fatal level and then exits.
func Fatalw(msg string, keysAndValues ...any) { l.Fatalw(msg, keysAndValues...) }
