package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	isDevelopment = false // if running in debug mode

	logFile *os.File = nil

	AdHocLogger zerolog.Logger

	once sync.Once

	globalLogger zerolog.Logger
)

func init() {
	// Create a general logger that can be easily accessed for
	// when you do not want to create a new logger
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	AdHocLogger = zerolog.New(os.Stderr).With().Timestamp().Str("service", "ad-hoc-logger").Caller().Logger()
}

func GetLogger(serviceName string) zerolog.Logger {

	once.Do(func() {

		if !isDevelopment {
			globalLogger = zerolog.New(os.Stderr).With().Timestamp().Str("service", serviceName).Logger()
		}

		// Set up zerolog for development mode (human-readable logs)
		consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339,
			FormatLevel: func(i any) string {
				return strings.ToUpper(fmt.Sprintf("[%5s]", i))
			},
			FormatMessage: func(i any) string {
				return fmt.Sprintf("| %s |", i)
			},
			FormatCaller: func(i any) string {
				return filepath.Base(fmt.Sprintf("%s", i))
			},
			PartsExclude: []string{
				zerolog.TimestampFieldName,
			}}
		// Use multi-writer for file and readable console output
		multiDev := zerolog.MultiLevelWriter(consoleWriter, logFile)
		globalLogger = zerolog.New(multiDev).Level(zerolog.TraceLevel).With().Timestamp().Str("service", serviceName).Caller().Logger()
	})

	return globalLogger
}

func SetDevelopment(value bool) {
	isDevelopment = value
}

func SetLogFile(file *os.File) {
	logFile = file
}
