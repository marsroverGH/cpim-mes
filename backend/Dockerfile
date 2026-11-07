ARG RELEASE_VERSION=v0.42.1
ARG VCS_REF=unknown
ARG BUILD_TIME=unknown

FROM golang:1.26.10-alpine3.24 AS build
ARG RELEASE_VERSION
ARG VCS_REF
ARG BUILD_TIME
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly -trimpath \
    -ldflags="-s -w -X github.com/cpim-mes/backend/internal/buildinfo.Version=${RELEASE_VERSION} -X github.com/cpim-mes/backend/internal/buildinfo.Commit=${VCS_REF} -X github.com/cpim-mes/backend/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o /out/server ./cmd/server

FROM alpine:3.24
ARG RELEASE_VERSION
ARG VCS_REF
ARG BUILD_TIME
LABEL org.opencontainers.image.title="CPIM-MES Backend" \
      org.opencontainers.image.version="${RELEASE_VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_TIME}"
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates tzdata
COPY --from=build /out/server /usr/local/bin/server
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
