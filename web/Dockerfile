# The web UI (Next.js) for hosts that build from the repository root (Railway: RAILWAY_DOCKERFILE_PATH=web/Dockerfile).
# It serves pages only; Core proxies them (FACTORY_WEB_UPSTREAM) and serves /api and /auth itself.
FROM node:22-bookworm-slim AS build
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build
FROM node:22-bookworm-slim
WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOSTNAME=0.0.0.0
COPY --from=build /app ./
EXPOSE 3000
CMD ["npm", "start"]
