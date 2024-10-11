#!/bin/sh

contains() {
	key=$1
	shift
	for a in "$@"; do
		case "$a" in
		"$key"*) return 1 ;;
		esac
	done
	return 0
}

DEFAULT_NODE_ID=$(hostname)
DEFAULT_ADV_ADDRESS=$(hostname -f)

CMD=$1
ARGS="$@"

contains "-http-addr" $ARGS
if [ $? -eq 0 ]; then
	HTTP_ADDR="${HTTP_ADDR:-0.0.0.0:4001}"
	http_addr="--http-addr $HTTP_ADDR"
fi

contains "-http-adv-addr" $ARGS
if [ $? -eq 0 ]; then
	HTTP_ADV_ADDR="${HTTP_ADV_ADDR:-$DEFAULT_ADV_ADDRESS:4001}"
	http_adv_addr="--http-adv-addr $HTTP_ADV_ADDR"
fi

contains "-raft-addr" $ARGS
if [ $? -eq 0 ]; then
	RAFT_ADDR="${RAFT_ADDR:-0.0.0.0:4002}"
	raft_addr="--raft-addr $RAFT_ADDR"
fi

contains "-raft-adv-addr" $ARGS
if [ $? -eq 0 ]; then
	RAFT_ADV_ADDR="${RAFT_ADV_ADDR:-$DEFAULT_ADV_ADDRESS:4002}"
	raft_adv_addr="--raft-adv-addr $RAFT_ADV_ADDR"
fi

contains "-node-id" $ARGS
if [ $? -eq 0 ]; then
	NODE_ID="${NODE_ID:-$DEFAULT_NODE_ID}"
	node_id="--node-id $NODE_ID"
fi

# When running on Kubernetes, delay a small time so DNS records
# are configured across the cluster when this wire comes up. Because
# wire does node-discovery using a headless service, it must have
# accurate DNS records. If the Pods addresses are not in the records,
# the DNS lookup will result in an error, and the Kubernetes system will
# cache this failure for (by default) 30 seconds. So this delay
# actually means getting to "ready" is quicker.
#
# This is kind of a hack. If anyone knows a better way file
# a GitHub issue at https://github.com/tarungka/wire.
if [ -n "$KUBERNETES_SERVICE_HOST" ] && [ -z "$START_DELAY" ]; then
	START_DELAY=5
fi
[ -n "$START_DELAY" ] && sleep "$START_DELAY"

WIRE=/bin/wire
wire_commands="$WIRE $node_id $http_addr $http_adv_addr $raft_addr $raft_adv_addr"

# Check for two specific invocation commands. If neither is found, just run
# the command exactly as passed.
case "$CMD" in
run)
	# Default from Dockerfile
	set -- $wire_commands
	;;
-*)
	# User is passing some options, so merge them.
	set -- $wire_commands "$@"
	;;
esac

exec "$@"
