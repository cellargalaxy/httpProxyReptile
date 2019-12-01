FROM golang as builder
COPY . /root
WORKDIR /root
RUN go mod download
RUN go build -o proxyReptile main.go

FROM alpine
RUN apk --no-cache add ca-certificates
COPY --from=builder /root/proxyReptile /
RUN chmod +x /proxyReptile
ENTRYPOINT ["/proxyReptile"]