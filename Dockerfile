# syntax=docker/dockerfile:1

FROM golang:1.21 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 GOOS=linux go build -o /tailscale-operator-dns-shadow

FROM gcr.io/distroless/static-debian12@sha256:4a2c1a51ae5e10ec4758a0f981be3ce5d6ac55445828463fce8dff3a355e0b75 AS prod
COPY --from=builder /tailscale-operator-dns-shadow /usr/bin/

ENTRYPOINT ["/usr/bin/tailscale-operator-dns-shadow"]