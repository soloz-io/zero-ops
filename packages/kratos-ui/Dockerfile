# ─── Build stage ──────────────────────────────────────────────────────────────
FROM node:22-alpine AS builder

WORKDIR /app

RUN corepack enable && corepack prepare pnpm@11.1.3 --activate

# Disable pnpm supply-chain minimum release age check
# pnpm reads .npmrc from home dir, workdir, and parent dirs
RUN echo 'minimum-release-age=0' > ~/.npmrc

# Copy package manifest and lockfile first for better caching
COPY packages/kratos-ui/package.json packages/kratos-ui/pnpm-lock.yaml ./

# pnpm 11 requires allowBuilds for native packages like esbuild
RUN printf 'onlyBuiltDependencies:\n  - esbuild\nallowBuilds:\n  esbuild: true\n' > pnpm-workspace.yaml

RUN HUSKY=0 pnpm install --frozen-lockfile

# Copy frontend source
COPY packages/kratos-ui/ ./

ARG VITE_APP_NAME=nutgraf
ENV VITE_APP_NAME=${VITE_APP_NAME}

# VITE_KRATOS_URL is deliberately NOT set here.
#
# Vite inlines import.meta.env at build time, so anything set at this point is
# frozen into the shipped bundle and cannot be corrected by a manifest. Leaving it
# unset makes the app fall back to window.location.origin at runtime, which is
# correct in every environment: the console serves this app at '/' and the gateway
# routes '/self-service' to Kratos on the same host, so the requests are
# same-origin and need no CORS grant.
#
# Setting it would reintroduce the bug this replaced: the variable was unset, the
# config fell back to a localhost default, and every deployed build tried to reach
# http://localhost:4433 from an HTTPS page.

RUN pnpm build

# ─── Runtime stage ────────────────────────────────────────────────────────────
FROM nginx:1.27-alpine AS runtime

RUN rm /etc/nginx/conf.d/default.conf
COPY packages/kratos-ui/nginx.conf /etc/nginx/conf.d/default.conf

COPY --from=builder /app/dist /usr/share/nginx/html

RUN chown -R nginx:nginx /usr/share/nginx/html && \
    chown -R nginx:nginx /var/cache/nginx && \
    chown -R nginx:nginx /var/log/nginx && \
    touch /var/run/nginx.pid && \
    chown nginx:nginx /var/run/nginx.pid

USER nginx

EXPOSE 3000
