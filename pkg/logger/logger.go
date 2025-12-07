package logger

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// L is the global logger instance.
var L *zap.SugaredLogger

func init() {
	// Default to development logger - ensures logs work even if Init() is forgotten.
	// In production, Init() should be called to configure proper settings.
	L = zap.Must(zap.NewDevelopment()).Sugar()
}

// Init initializes the global logger.
// Call this at application startup.
func Init(development bool) {
	var config zap.Config
	if development {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05")
	} else {
		config = zap.NewProductionConfig()
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	// Apply log level from environment if set
	if levelStr := os.Getenv("LOG_LEVEL"); levelStr != "" {
		var level zapcore.Level
		if err := level.UnmarshalText([]byte(strings.ToLower(levelStr))); err == nil {
			config.Level = zap.NewAtomicLevelAt(level)
		}
	}

	// Log to stderr
	config.OutputPaths = []string{"stderr"}
	config.ErrorOutputPaths = []string{"stderr"}

	logger, err := config.Build(zap.AddCallerSkip(1))
	if err != nil {
		// Fallback to no-op logger
		L = zap.NewNop().Sugar()
		return
	}

	L = logger.Sugar()
}

// InitFromEnv initializes the logger based on environment.
// Uses development mode if DEV=true.
// Log level can be set via LOG_LEVEL (debug, info, warn, error).
func InitFromEnv() {
	dev := os.Getenv("DEV") == "true"
	Init(dev)
}

// Sync flushes any buffered log entries.
// Call this before application exit.
func Sync() {
	if L != nil {
		_ = L.Sync()
	}
}

// With returns a logger with additional context fields.
func With(keysAndValues ...any) *zap.SugaredLogger {
	return L.With(keysAndValues...)
}

// Convenience functions that use the global logger

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
