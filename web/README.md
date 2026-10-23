# App Factory web UI

Next.js app for the owner (openspec change `owner-web`). The browser talks only to this app; it proxies
`/api/*` to `factory web` (Go), which holds the owner token.

```sh
go run ./cmd/factory web --demo          # API on 127.0.0.1:8090 with a recorded demo company (no Core)
cd web && npm ci && npm run dev          # http://localhost:3000
```

Live: `factory web` with FACTORY_URL/FACTORY_TOKEN, then `npm run build && npm start -- -H <tailscale IP>`.
Set `FACTORY_WEB_API` when the API is not on 127.0.0.1:8090.
