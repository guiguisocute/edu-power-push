FROM node:22-alpine AS build

WORKDIR /src
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

FROM caddy:2.10-alpine

RUN setcap -r /usr/bin/caddy
COPY Caddyfile /etc/caddy/Caddyfile
# 产物落在 /srv/dist 而不是 /srv：容器里的 Caddyfile、文档里的反代示例
# 三处写的是同一个路径，看哪一处都不会推翻另外两处。
COPY --from=build /src/dist /srv/dist
USER 65532:65532
EXPOSE 8080
