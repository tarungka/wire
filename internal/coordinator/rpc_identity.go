package coordinator

import (
	"context"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type workerCertificateKey struct{}

func checkWorkerIdentity(ctx context.Context, method rpc.MethodID, payload []byte) *rpc.RPCError {
	name, authenticated := ctx.Value(workerCertificateKey{}).(string)
	if !authenticated {
		return nil
	} // Plaintext/local development mode is not authenticated.
	var identity struct {
		WorkerID        string `codec:"wid"`
		ReplicaWorkerID string `codec:"rwid"`
	}
	if err := protocol.DecodeMsgPack(payload, &identity); err != nil {
		return rpc.NewRPCError(rpc.ErrCodeSerializationError, err.Error())
	}
	claimed := identity.WorkerID
	if method == rpc.MethodAuthorizeCheckpointFetch {
		claimed = identity.ReplicaWorkerID
	}
	if name == "" || claimed != name {
		return rpc.NewRPCError(rpc.ErrCodeInvalidRequest, "worker identity does not match verified client certificate")
	}
	return nil
}
func guardWorkerRPC(method rpc.MethodID, handler rpc.Handler) rpc.Handler {
	return func(ctx context.Context, id uint64, payload []byte) (any, *rpc.RPCError) {
		if err := checkWorkerIdentity(ctx, method, payload); err != nil {
			return nil, err
		}
		return handler(ctx, id, payload)
	}
}
