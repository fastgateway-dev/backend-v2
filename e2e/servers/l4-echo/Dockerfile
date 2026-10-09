FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -tags e2e -a -installsuffix cgo -o l4-echo .

FROM alpine:3.19
WORKDIR /app
RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /app/l4-echo .
EXPOSE 9100
EXPOSE 9101/udp
CMD ["./l4-echo"]
