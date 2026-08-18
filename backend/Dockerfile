FROM golang:1.23-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/offlag-server .

FROM python:3.12-slim-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libsqlite3-0 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY requirements.txt ./
RUN python -m pip install --no-cache-dir -r requirements.txt

COPY --from=builder /out/offlag-server /usr/local/bin/offlag-server
COPY get_uuid_3xui.py stats_3xui.py sync_3xui.py ./

ENV PORT=8080 \
    DB_PATH=/data/offlag.db \
    PYTHON_BIN=python3 \
    PYTHON_SCRIPT_DIR=/app

RUN useradd --create-home --uid 10001 offlag \
    && mkdir -p /data \
    && chown -R offlag:offlag /data /app

USER offlag
VOLUME ["/data"]
EXPOSE 8080

CMD ["offlag-server"]
