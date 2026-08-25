# ─── Build stage ──────────────────────────────────────────────────────────────
FROM node:22-alpine AS builder

WORKDIR /app

RUN corepack enable && corepack prepare pnpm@11.1.3 --activate

# Copy package manifest and lockfile first for better caching
COPY packages/kratos-ui/package.json packages/kratos-ui/pnpm-lock.yaml ./pnpm/

# pnpm 11 requires allowBuilds for native packages like esbuild
RUN printf 'onlyBuiltDependencies:\n  - esbuild\nallowBuilds:\n  esbuild: true\n' > pnpm-workspace.yaml

# Disable pnpm supply-chain minimum release age check
RUN echo 'minimumReleaseAge=0' > .npmrc

RUN cd pnpm && HUSKY=0 pnpm install --frozen-lockfile

# Copy frontend source
COPY packages/kratos-ui/ ./pnpm/

# VITE_KRATOS_URL baked into the static bundle at build time
ARG VITE_KRATOS_URL=https://auth.nutgraf.in
ENV VITE_KRATOS_URL=${VITE_KRATOS_URL}

ARG VITE_APP_NAME=nutgraf
ENV VITE_APP_NAME=${VITE_APP_NAME}

RUN cd pnpm && pnpm build

# ─── Runtime stage ────────────────────────────────────────────────────────────
FROM nginx:1.27-alpine AS runtime

RUN rm /etc/nginx/conf.d/default.conf
COPY packages/kratos-ui/nginx.conf /etc/nginx/conf.d/default.conf

COPY --from=builder /app/pnpm/dist /usr/share/nginx/html

RUN chown -R nginx:nginx /usr/share/nginx/html && \
    chown -R nginx:nginx /var/cache/nginx && \
    chown -R nginx:nginx /var/log/nginx && \
    touch /var/run/nginx.pid && \
    chown nginx:nginx /var/run/nginx.pid

USER nginx

EXPOSE 3000
