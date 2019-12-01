FROM golang as builder
COPY main.go /
WORKDIR /
RUN go get main.go
RUN go build -o proxyReptile main.go
#RUN go run main.go -no-upgrade build proxyReptile

FROM alpine
RUN apk --no-cache add ca-certificates
COPY --from=builder /proxyReptile /
RUN chmod +x /proxyReptile
ENTRYPOINT ["/proxyReptile"]