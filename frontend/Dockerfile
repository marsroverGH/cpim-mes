ARG RELEASE_VERSION=v0.43.0
ARG VCS_REF=unknown
ARG BUILD_TIME=unknown

FROM node:24.20.0-alpine3.24 AS build
ARG RELEASE_VERSION
ARG VCS_REF
ARG BUILD_TIME
WORKDIR /app
RUN npm install --global npm@11.19.0
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
ENV VITE_APP_VERSION=${RELEASE_VERSION} \
    VITE_APP_COMMIT=${VCS_REF} \
    VITE_APP_BUILD_TIME=${BUILD_TIME}
RUN npm run build

FROM nginx:1.30.6-alpine3.24 AS runtime
ARG RELEASE_VERSION
ARG VCS_REF
ARG BUILD_TIME
LABEL org.opencontainers.image.title="CPIM-MES Frontend" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_TIME}"
RUN apk upgrade --no-cache
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1
