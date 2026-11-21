FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /chronomorph .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /chronomorph /usr/local/bin/chronomorph
ENTRYPOINT ["/usr/local/bin/chronomorph"]
CMD ["server", "-c", "/etc/chronomorph/server.json"]
