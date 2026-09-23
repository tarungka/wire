package config

import (
	"fmt"
	"os"
	"reflect"
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

// envSubstConfig visits every string, including strings inside lists. Walking
// the configuration shape prevents newly added fields from silently missing
// substitution. Duration and numeric fields deliberately remain typed values.
func envSubstConfig(cfg *WireConfig) error {
	return substConfigValue(reflect.ValueOf(cfg).Elem(), "")
}

func substConfigValue(value reflect.Value, path string) error {
	switch value.Kind() {
	case reflect.String:
		result, err := EnvSubst(value.String())
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		value.SetString(result)
	case reflect.Struct:
		if value.Type() == reflect.TypeOf(Duration{}) {
			return nil
		}
		for i := 0; i < value.NumField(); i++ {
			name := value.Type().Field(i).Tag.Get("koanf")
			if path != "" {
				name = path + "." + name
			}
			if err := substConfigValue(value.Field(i), name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if err := substConfigValue(value.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
