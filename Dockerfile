FROM golang:1.22 as builder

WORKDIR /app

COPY go.mod .
COPY go.sum .
COPY cmd cmd
COPY internal internal

RUN CGO_ENABLED=0 go build -ldflags="-extldflags=-static" -o server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags="-extldflags=-static" -o client ./cmd/client
RUN CGO_ENABLED=0 go build -ldflags="-extldflags=-static" -o control-plane ./cmd/control-plane/main
RUN CGO_ENABLED=0 go build -ldflags="-extldflags=-static" -o control-plane-kube ./cmd/control-plane-kube/main

FROM scratch

COPY --from=builder /app/ /
