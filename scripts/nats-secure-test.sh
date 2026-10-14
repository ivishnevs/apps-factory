#!/bin/sh
# nats-secure-test.sh up|down [DIR]: throwaway NATS servers for the node's outbound-connection tests
# (node-agent 1.5). Everything lives in DIR (default ./.nats-secure-test) and in tmpfs inside the containers.
#
#   factory-nats-secure: tls:// on 127.0.0.1:14222 and wss:// on 127.0.0.1:14443 (server name nats.test,
#                        private CA), nkey users: admin (all) and node (the node permission set below).
#   factory-nats-creds:  operator mode (memory resolver), plain nats:// on 127.0.0.1:14223, a JWT user in node.creds.
#
# `up` prints the NATS_TEST_* exports the gated tests in internal/bus and internal/node read.
set -eu
cmd=${1:-up}
dir=${2:-.nats-secure-test}
box=natsio/nats-box:0.17.0
image=nats:2.11.4-alpine

if [ "$cmd" = down ]; then
	docker rm -f factory-nats-secure factory-nats-creds >/dev/null 2>&1 || true
	rm -rf "$dir"
	exit 0
fi

rm -rf "$dir"
mkdir -p "$dir"
dir=$(cd "$dir" && pwd)
chmod 700 "$dir"

# Private CA and a server certificate for nats.test and 127.0.0.1.
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj /CN=factory-test-ca \
	-keyout "$dir/ca-key.pem" -out "$dir/ca.pem" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -subj /CN=nats.test -keyout "$dir/server-key.pem" -out "$dir/server.csr" 2>/dev/null
printf 'subjectAltName=DNS:nats.test,IP:127.0.0.1\n' >"$dir/san.ext"
openssl x509 -req -in "$dir/server.csr" -CA "$dir/ca.pem" -CAkey "$dir/ca-key.pem" -CAcreateserial \
	-days 2 -extfile "$dir/san.ext" -out "$dir/server.pem" 2>/dev/null
chmod 644 "$dir/server.pem" "$dir/server-key.pem" "$dir/ca.pem" # read by the server's user in the container

# nkey users: `nsc generate nkey --user` prints the seed, then the public key.
nkey() { docker run --rm "$box" nsc generate nkey --user 2>&1; }  # nsc prints the keys on stderr
nkey >"$dir/admin.nk"
nkey >"$dir/node.nk"
head -1 "$dir/admin.nk" >"$dir/admin.seed"
head -1 "$dir/node.nk" >"$dir/node.seed"
chmod 600 "$dir/admin.seed" "$dir/node.seed"
admin_pub=$(sed -n 2p "$dir/admin.nk")
node_pub=$(sed -n 2p "$dir/node.nk")
rm -f "$dir/admin.nk" "$dir/node.nk"

cat >"$dir/secure.conf" <<EOF
listen: 0.0.0.0:4222
jetstream { store_dir: /js }
tls { cert_file: /conf/server.pem, key_file: /conf/server-key.pem }
websocket { port: 8443, tls { cert_file: /conf/server.pem, key_file: /conf/server-key.pem } }
authorization {
  users = [
    { nkey: $admin_pub }
    # What a node needs today (runtime protocol v1/v2 on factory.commands.agent.run.*): stream info, its durable
    # pull consumers on FACTORY_COMMANDS, acks, agent events. No stream creation, no publishing commands.
    {
      nkey: $node_pub
      permissions: {
        publish: {
          allow: [
            "\$JS.API.INFO"
            "\$JS.API.STREAM.NAMES"
            "\$JS.API.STREAM.INFO.FACTORY_COMMANDS"
            "\$JS.API.STREAM.INFO.FACTORY_EVENTS"
            "\$JS.API.CONSUMER.CREATE.FACTORY_COMMANDS.>"
            "\$JS.API.CONSUMER.DURABLE.CREATE.FACTORY_COMMANDS.>"
            "\$JS.API.CONSUMER.INFO.FACTORY_COMMANDS.>"
            "\$JS.API.CONSUMER.MSG.NEXT.FACTORY_COMMANDS.>"
            "\$JS.ACK.FACTORY_COMMANDS.>"
            "factory.events.agent.>"
          ]
        }
        subscribe: { allow: [ "_INBOX.>" ] }
      }
    }
  ]
}
EOF

# Operator mode for a .creds user: operator, system account, account FACTORY with JetStream, user node.
docker run --rm -v "$dir:/out" "$box" sh -ec '
	export NKEYS_PATH=/tmp/nk NSC_HOME=/tmp/nsc
	nsc env -s /tmp/store >/dev/null 2>&1
	nsc add operator -n test --sys >/dev/null 2>&1
	nsc add account -n FACTORY >/dev/null 2>&1
	nsc edit account -n FACTORY --js-mem-storage -1 --js-disk-storage -1 --js-streams -1 --js-consumer -1 >/dev/null 2>&1
	nsc add user -a FACTORY -n node >/dev/null 2>&1
	nsc generate config --mem-resolver --config-file /out/resolver.conf >/dev/null 2>&1
	nsc generate creds -a FACTORY -n node >/out/node.creds 2>/dev/null
	chmod 644 /out/resolver.conf; chmod 600 /out/node.creds'
cat >"$dir/creds.conf" <<EOF
listen: 0.0.0.0:4222
jetstream { store_dir: /js }
include resolver.conf
EOF

docker rm -f factory-nats-secure factory-nats-creds >/dev/null 2>&1 || true
docker run -d --name factory-nats-secure --tmpfs /js:size=512m -v "$dir:/conf:ro" \
	-p 127.0.0.1:14222:4222 -p 127.0.0.1:14443:8443 "$image" -c /conf/secure.conf >/dev/null
docker run -d --name factory-nats-creds --tmpfs /js:size=512m -v "$dir:/conf:ro" \
	-p 127.0.0.1:14223:4222 "$image" -c /conf/creds.conf >/dev/null
sleep 1
for c in factory-nats-secure factory-nats-creds; do
	if [ "$(docker inspect -f '{{.State.Running}}' $c)" != true ]; then
		docker logs $c >&2
		exit 1
	fi
done

cat <<EOF
export NATS_TEST_SECURE_TLS_URL=tls://127.0.0.1:14222
export NATS_TEST_SECURE_WSS_URL=wss://127.0.0.1:14443
export NATS_TEST_SECURE_CA=$dir/ca.pem
export NATS_TEST_SECURE_SERVER_NAME=nats.test
export NATS_TEST_ADMIN_SEED=$dir/admin.seed
export NATS_TEST_NODE_SEED=$dir/node.seed
export NATS_TEST_CREDS_URL=nats://127.0.0.1:14223
export NATS_TEST_CREDS_FILE=$dir/node.creds
EOF
