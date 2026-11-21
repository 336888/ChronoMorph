FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /slacker .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /slacker /usr/local/bin/slacker
ENTRYPOINT ["/usr/local/bin/slacker"]
CMD ["server", "-c", "/etc/chronomorph/server.json"]
