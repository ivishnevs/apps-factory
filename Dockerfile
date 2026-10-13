FROM golang:1.24.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /data && chmod 700 /data
RUN CGO_ENABLED=0 go build -trimpath -o /factory ./cmd/factory
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /factory /usr/local/bin/factory
COPY --from=build --chown=65532:65532 /data /data
COPY configs /app/configs
COPY prompts /app/prompts
COPY skills /app/skills
ENTRYPOINT ["factory"]
CMD ["serve"]
