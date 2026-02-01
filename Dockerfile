FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /localcinema

FROM alpine:3.20
RUN apk add --no-cache ffmpeg
COPY --from=builder /localcinema /usr/local/bin/localcinema
EXPOSE 8080
VOLUME /videos
ENTRYPOINT ["localcinema", "-dir", "/videos"]
