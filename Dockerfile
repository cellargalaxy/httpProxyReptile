FROM golang AS builder
WORKDIR /src
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /src/proxyReptile

FROM alpine
RUN apk --no-cache add ca-certificates
COPY --from=builder /src/proxyReptile /proxyReptile
CMD ["/proxyReptile"]