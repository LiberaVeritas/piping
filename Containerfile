FROM golang:1.26-alpine3.24 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN mkdir out
RUN CGO_ENABLED=0 go build -o /out/ piping/cmd/piping piping/cmd/rollover

FROM alpine:3.24
#WORKDIR /app
COPY --from=builder /out/ /usr/local/bin/
RUN apk add --no-cache ghostscript samba-client ca-certificates && adduser -D -H piping
EXPOSE 8080
USER piping
CMD ["piping"]
