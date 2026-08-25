# ─── Build stage ──────────────────────────────────────────────────────────────
FROM node:22-alpine AS builder

WORKDIR /app

RUN corepack enable && corepack prepare pnpm@11.1.3 --activate

# Copy only the package manifest (no lockfile — like the reference Next.js build).
# pnpm 11.1.3 supply-chain policy blocks lockfile entries published within 24 h;
# running without --frozen-lockfile avoids that and resolves fresh.
COPY packages/kratos-ui/package.json ./package.json

# pnpm 11 requires allowBuilds for native packages like esbuild
RUN printf 'onlyBuiltDependencies:\n  - esbuild\nallowBuilds:\n  esbuild: true\n' > pnpm-workspace.yaml

RUN HUSKY=0 pnpm install --no-frozen-lockfile

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
