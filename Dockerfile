FROM golang:1.26.0-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker

FROM debian:bookworm-slim AS api
COPY --from=build /out/api /usr/local/bin/infoex-run-api
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/infoex-run-api"]

FROM docker:28-cli AS worker
COPY --from=build /out/worker /usr/local/bin/infoex-run-worker
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/infoex-run-worker"]
