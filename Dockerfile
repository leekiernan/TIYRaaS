FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app
COPY main.go .
COPY reddit/ ./reddit/
COPY instagram/ ./instagram/

RUN go mod init tiktok-proxy && \
    go get github.com/sweepies/tok-dl@main && \
    go get github.com/charmbracelet/log && \
    go mod tidy

RUN go build -o main .

# ---

FROM alpine:latest
RUN apk add --no-cache curl
WORKDIR /app

RUN mkdir -p /app/cache

COPY --from=builder /app/main .
EXPOSE 8080
CMD ["./main"]
