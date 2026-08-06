FROM golang:1.26-alpine3.24 AS builder

WORKDIR /app
ENV GOEXPERIMENT=jsonv2
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -o piping piping/cmd/piping

FROM alpine:3.24
ENV TZ="America/Toronto"
COPY --from=builder /app/piping /usr/local/bin/
RUN apk add --no-cache ghostscript samba-client ca-certificates && adduser -D -H piping
EXPOSE 8080
USER piping
CMD ["piping"]
