# syntax=docker/dockerfile:1.7
FROM golang:1.26.7-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/btc-server ./cmd/btc-server

FROM debian:bookworm-slim AS epubcheck
ARG EPUBCHECK_VERSION=5.3.0
ARG EPUBCHECK_SHA256=6c07e68584b2e2ce2f89fe06e1246dfead3eb36b46b340e7d93524f29dcff6c5
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && curl -fsSL -o /tmp/epubcheck.zip "https://github.com/w3c/epubcheck/releases/download/v${EPUBCHECK_VERSION}/epubcheck-${EPUBCHECK_VERSION}.zip" \
    && echo "${EPUBCHECK_SHA256}  /tmp/epubcheck.zip" | sha256sum -c - \
    && unzip -q /tmp/epubcheck.zip -d /opt \
    && mv "/opt/epubcheck-${EPUBCHECK_VERSION}" /opt/epubcheck

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates openjdk-17-jre-headless \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 pdf2epub \
    && useradd --system --uid 10001 --gid pdf2epub --home-dir /nonexistent --shell /usr/sbin/nologin pdf2epub \
    && mkdir -p /var/lib/pdf2epub \
    && chown pdf2epub:pdf2epub /var/lib/pdf2epub
COPY --from=builder /out/btc-server /usr/local/bin/btc-server
COPY --from=epubcheck /opt/epubcheck /opt/epubcheck
COPY deploy/epubcheck /usr/local/bin/epubcheck
RUN chmod 0555 /usr/local/bin/btc-server /usr/local/bin/epubcheck
USER 10001:10001
ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    BTC_ADDR=0.0.0.0:8080 \
    BTC_WORK_DIR=/var/lib/pdf2epub \
    BTC_EPUBCHECK_COMMAND=/usr/local/bin/epubcheck \
    BTC_REQUIRE_EPUBCHECK=true
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/btc-server"]
