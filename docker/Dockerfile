FROM golang:1.26.1-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd/probe ./cmd/probe
RUN CGO_ENABLED=0 go build -o /probe ./cmd/probe

FROM debian:bookworm-slim AS base
COPY --from=build /probe /probe
COPY docker/wrapper.sh docker/exec-wrapper.sh /
RUN chmod +x /wrapper.sh /exec-wrapper.sh
STOPSIGNAL SIGTERM

FROM base AS direct
ENTRYPOINT ["/probe", "parent"]

FROM base AS shell
ENTRYPOINT /probe parent

FROM base AS wrapper
ENTRYPOINT ["/wrapper.sh"]

FROM base AS exec-wrapper
ENTRYPOINT ["/exec-wrapper.sh"]
