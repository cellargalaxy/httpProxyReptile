FROM golang as builder
COPY main.go /
WORKDIR /
RUN go build -o proxyReptile main.go

FROM alpine
RUN apk --no-cache add ca-certificates
COPY --from=builder /proxyReptile /
ENTRYPOINT ["/proxyReptile"]