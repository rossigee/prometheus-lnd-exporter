FROM golang:1.24 AS builder

ARG VERSION
ARG GIT_COMMIT

WORKDIR /app/

COPY . .

RUN go build -a -installsuffix cgo -ldflags "-X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" -o lnd-exporter .

FROM alpine:latest

RUN apk update && apk add ca-certificates

COPY --from=builder /app/lnd-exporter /app/

ENTRYPOINT [ "/app/lnd-exporter" ]
