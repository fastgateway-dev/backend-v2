FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o ext-proc-server .

FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/ext-proc-server /usr/local/bin/ext-proc-server

EXPOSE 9004

ENTRYPOINT ["ext-proc-server"]
