FROM golang:alpine AS builder
ARG VERSION
ARG GIT_COMMIT
RUN apk add gcc musl-dev

WORKDIR /app/
COPY . .
ENV CGO_ENABLED=1
RUN go mod download && go build -ldflags "-X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" -o lnd-exporter .

FROM alpine:latest
RUN apk add --no-cache ca-certificates libc6-compat
COPY --from=builder /app/lnd-exporter /app/

ENTRYPOINT [ "/app/lnd-exporter" ]
