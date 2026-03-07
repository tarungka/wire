package config

import "errors"

var (
	// ErrConfigFileLoad is returned when a config file cannot be read or parsed.
	ErrConfigFileLoad = errors.New("config file load error")

	// ErrConfigValidation is returned when configuration validation fails.
	ErrConfigValidation = errors.New("config validation error")

	// ErrEnvVarNotSet is returned when a ${VAR} reference has no default and the
	// environment variable is not set.
	ErrEnvVarNotSet = errors.New("environment variable not set")
)
