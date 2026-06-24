# syntax=docker/dockerfile:1

# --- Build stage ----------------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/ccgate .

# --- Runtime stage --------------------------------------------------------
# Node provides the local tokenizer used when the upstream lacks count_tokens.
FROM node:22-bookworm-slim AS runtime
ENV CCGW_CONFIG_DIR=/data \
    CCGW_LISTEN=0.0.0.0:8787
RUN useradd --system --uid 10001 --create-home --home-dir /home/ccgate ccgate \
    && mkdir -p /data && chown ccgate:ccgate /data
COPY --from=build /out/ccgate /usr/local/bin/ccgate
USER ccgate
# Pre-install the tokenizer at build time so the first count works offline
# (no runtime npm). This is skipped automatically if the upstream supports
# count_tokens (passthrough mode).
RUN ccgate setup-tokenizer
EXPOSE 8787
ENTRYPOINT ["ccgate"]
CMD ["run"]
