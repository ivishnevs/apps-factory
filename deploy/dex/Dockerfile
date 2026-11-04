# Dex with App Factory's config baked in, for hosts that build services from the repository (Railway: set the
# service's Dockerfile path to deploy/dex/Dockerfile). Settings come from the environment, see config.yaml;
# DEX_STORAGE=memory needs no database.
FROM ghcr.io/dexidp/dex:v2.43.1
COPY deploy/dex/config.yaml /etc/dex/config.docker.yaml
