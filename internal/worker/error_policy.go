package worker

import (
	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/errorpolicy"
	"github.com/tarungka/wire/internal/rpc"
)

func compileErrorPolicy(p *rpc.ErrorPolicy, name string) (engine.ErrorHandlerConfig, error) {
	return errorpolicy.Compile(p, name)
}
