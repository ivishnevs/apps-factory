# Dex with App Factory's config baked in, for hosts that build services from the repository (Railway: set the
# service's Dockerfile path to deploy/dex/Dockerfile). Settings come from the environment, see config.yaml.
# Dex keeps its state (signing keys, sign-ins in progress) in Postgres; ensure-db.sh creates its database on first
# start. DEX_STORAGE=memory needs no database.
FROM ghcr.io/dexidp/dex:v2.46.1
USER root
RUN apk add --no-cache postgresql17-client
COPY deploy/dex/config.yaml /etc/dex/config.docker.yaml
COPY --chmod=0755 deploy/dex/ensure-db.sh /usr/local/bin/ensure-db
USER 1001:1001
ENTRYPOINT ["/usr/local/bin/ensure-db"]
CMD ["dex", "serve", "/etc/dex/config.docker.yaml"]
