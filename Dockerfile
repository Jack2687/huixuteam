FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
ENV GOMAXPROCS=2 GOFLAGS=-p=2
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY webui ./webui
COPY --from=frontend /src/webui/static ./webui/static
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM python:3.11-slim-bookworm
ARG VCS_REF=local
LABEL org.opencontainers.image.title="HUIXU" org.opencontainers.image.revision=$VCS_REF
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata nodejs npm && rm -rf /var/lib/apt/lists/* && useradd -m -u 10001 app
COPY runtime/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt
COPY runtime /app/runtime
# Git checkout may run with umask 077. Source must remain readable by the
# unprivileged runtime user, independently of build-context file permissions.
RUN chmod -R a+rX /app/runtime && cd /app/runtime/manager_oauth && npm ci --omit=dev --no-audit --no-fund
USER app
WORKDIR /app
RUN python /app/runtime/refund_runner.py --check && python -m unittest discover -s /app/runtime -p 'test_*.py'
COPY --from=backend /server /app/server
ENV LISTEN_HOST=0.0.0.0 PORT=18740
EXPOSE 18740
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s CMD python -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:18740/health/ready', timeout=2)" || exit 1
CMD ["/app/server"]
