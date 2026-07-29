# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.2

FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -buildvcs=false \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/solvify-agent \
    ./cmd/server

FROM python:3.13-slim-bookworm AS runtime

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    CONFIG_PATH=/app/configs/config.yaml \
    DOCUMENT_PARSER_PYTHON_PATH=python \
    DOCUMENT_PARSER_SCRIPT_PATH=/app/pkg/documentparser/python/parse_document.py

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY pkg/documentparser/python/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir --disable-pip-version-check -r /tmp/requirements.txt \
    && rm -f /tmp/requirements.txt

RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --create-home --shell /usr/sbin/nologin app \
    && mkdir -p /app/configs /app/data/uploads /app/logs /app/pkg/documentparser/python \
    && chown -R app:app /app

COPY --from=builder --chown=app:app /out/solvify-agent /app/solvify-agent
COPY --chown=app:app pkg/documentparser/python/parse_document.py /app/pkg/documentparser/python/parse_document.py

USER app

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8080/health', timeout=3)"]

ENTRYPOINT ["/app/solvify-agent"]

