package config

import (
	"fmt"
	"os"
	"regexp"
)

// envPattern matches ${VAR} and ${VAR:-default} patterns.
var envPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::-(.*?))?\}`)

// EnvSubst replaces environment variable references in s.
//
// Supported patterns:
//
//	${VAR}           — replaced by the value of VAR; error if unset
//	${VAR:-default}  — replaced by the value of VAR, or "default" if unset
//	${VAR:-}         — replaced by the value of VAR, or "" if unset
func EnvSubst(s string) (string, error) {
	var firstErr error
	result := envPattern.ReplaceAllStringFunc(s, func(match string) string {
		if firstErr != nil {
			return match
		}
		groups := envPattern.FindStringSubmatch(match)
		varName := groups[1]
		hasDefault := len(groups) > 2 && groups[2] != "" || // has non-empty default
			len(match) > len("${"+varName+"}") // has :- syntax (even empty default)

		val, ok := os.LookupEnv(varName)
		if ok {
			return val
		}
		if hasDefault {
			return groups[2]
		}
		firstErr = fmt.Errorf("%w: %s", ErrEnvVarNotSet, varName)
		return match
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

// envSubstConfig applies environment variable substitution to all string
// fields in cfg. Fields are enumerated explicitly (no reflection).
func envSubstConfig(cfg *WireConfig) error {
	fields := []*string{
		&cfg.Node.ID,
		&cfg.Node.DataDir,
		&cfg.Node.StoreDB,
		&cfg.HTTP.Addr,
		&cfg.HTTP.AdvAddr,
		&cfg.HTTP.AllowOrigin,
		&cfg.HTTP.TLS.Cert,
		&cfg.HTTP.TLS.Key,
		&cfg.HTTP.TLS.CACert,
		&cfg.HTTP.TLS.VerifyServerName,
		&cfg.NodeTLS.Cert,
		&cfg.NodeTLS.Key,
		&cfg.NodeTLS.CACert,
		&cfg.NodeTLS.VerifyServerName,
		&cfg.Auth.File,
		&cfg.Election.Backend,
		&cfg.Election.LockPath,
	}
	for _, fp := range fields {
		if *fp == "" {
			continue
		}
		val, err := EnvSubst(*fp)
		if err != nil {
			return err
		}
		*fp = val
	}
	return nil
}
