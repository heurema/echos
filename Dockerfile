# Build the relay binary. The client CLI is distributed separately; this
# image ships only cmd/echos-relay.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/echos-relay ./cmd/echos-relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/echos-relay /echos-relay
# bbolt store lives on a mounted volume so blobs survive restarts
ENV ECHOS_RELAY_DB=/data/echos-relay.db
EXPOSE 8080
ENTRYPOINT ["/echos-relay"]
