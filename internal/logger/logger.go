package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

var (
	isDevelopment = false // if running in debug mode

	logFile *os.File = nil
)

func GetLogger(serviceName string) zerolog.Logger {

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	if !isDevelopment {
		return zerolog.New(os.Stderr).With().Timestamp().Str("service", serviceName).Logger()
	}

	// Set up zerolog for development mode (human-readable logs)
	consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339,
		FormatLevel: func(i interface{}) string {
			return strings.ToUpper(fmt.Sprintf("[%5s]", i))
		},
		FormatMessage: func(i interface{}) string {
			return fmt.Sprintf("| %s |", i)
		},
		FormatCaller: func(i interface{}) string {
			return filepath.Base(fmt.Sprintf("%s", i))
		},
		PartsExclude: []string{
			zerolog.TimestampFieldName,
		}}
	// Use multi-writer for file and readable console output
	multiDev := zerolog.MultiLevelWriter(consoleWriter, logFile)
	return zerolog.New(multiDev).Level(zerolog.TraceLevel).With().Timestamp().Str("service", serviceName).Caller().Logger()
}

func SetDevelopment(value bool) {
	isDevelopment = value
}

func SetLogFile(file *os.File) {
	logFile = file
}