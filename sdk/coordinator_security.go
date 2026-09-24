package sdk

// CoordinatorSecurity configures only the submitting application's HTTP client.
// File contents are loaded at execution time and never enter the job graph.
// Supplying any credential or TLS option requires an HTTPS coordinator URL.
// Choose APIKeyFile or Username/PasswordFile, not both. No redirects are followed.
type CoordinatorSecurity struct {
	CACert, ClientCert, ClientKey      string
	APIKeyFile, Username, PasswordFile string
}

// SetCoordinatorSecurity configures HTTPS trust and authentication for submission
// and status polling. It does not configure workers or coordinator listeners.
func (env *StreamExecutionEnvironment) SetCoordinatorSecurity(config CoordinatorSecurity) *StreamExecutionEnvironment {
	env.coordinatorSecurity = config
	return env
}
