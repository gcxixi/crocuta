FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/sentryx-server ./cmd/sentryx-server \
 && CGO_ENABLED=0 go build -o /out/sentryx-relay ./cmd/sentryx-relay

FROM alpine:3.21
RUN addgroup -S sentryx && adduser -S -G sentryx sentryx
COPY --from=build /out/sentryx-server /sentryx-server
COPY --from=build /out/sentryx-relay /sentryx-relay
USER sentryx
