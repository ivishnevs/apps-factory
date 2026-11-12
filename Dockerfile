FROM golang:1.26.0-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /data /blobs && chmod 700 /data /blobs
RUN CGO_ENABLED=0 go build -trimpath -o /factory ./cmd/factory
# The node packages this Core hands out (node-install D1): built with Node.js for the Claude shim.
FROM golang:1.26.0-bookworm AS nodepkg
# Empty: scripts/package-node.sh versions the packages by build time (node-packaging-updates C).
ARG NODE_VERSION=
COPY --from=node:22-bookworm-slim /usr/local/bin/node /usr/local/bin/node
COPY --from=node:22-bookworm-slim /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf workers/claude/node_modules workers/claude/dist && scripts/package-node.sh /out "$NODE_VERSION"
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /factory /usr/local/bin/factory
COPY --from=build --chown=65532:65532 /data /data
COPY --from=build --chown=65532:65532 /blobs /blobs
COPY configs /app/configs
COPY prompts /app/prompts
COPY skills /app/skills
COPY presets /app/presets
COPY --from=nodepkg /out /app/dist/node
ENTRYPOINT ["factory"]
CMD ["serve"]
