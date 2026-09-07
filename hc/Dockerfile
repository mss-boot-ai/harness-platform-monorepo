# syntax=docker/dockerfile:1.7

FROM node:24.19.0-bookworm-slim@sha256:3638d9a6fe4030bd716be989438248074489337ba3275657f93595428be4fc03 AS build
ARG VITE_HARNESS_DEFAULT_RUNTIME_PROFILE_ID=deepseek-harness
ARG VITE_HARNESS_DEFAULT_WORKSPACE_ID=harness-platform
ENV VITE_HARNESS_DEFAULT_RUNTIME_PROFILE_ID=$VITE_HARNESS_DEFAULT_RUNTIME_PROFILE_ID \
    VITE_HARNESS_DEFAULT_WORKSPACE_ID=$VITE_HARNESS_DEFAULT_WORKSPACE_ID
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/core/package.json packages/core/package.json
COPY web/package.json web/package.json
RUN corepack pnpm@10.34.5 install --frozen-lockfile
COPY tsconfig.json vitest.config.ts ./
COPY packages packages
COPY web web
RUN corepack pnpm@10.34.5 --filter @harness/hc-web build

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
RUN apk add --no-cache nginx
COPY web/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /src/web/dist /usr/share/nginx/html
USER 101:101
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --quiet --spider http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["nginx", "-g", "daemon off;"]
