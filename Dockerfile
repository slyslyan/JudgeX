# Stage 0: Build frontend
FROM node:22-alpine AS frontend-builder
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 1: Build Go binaries
FROM golang:1.24-alpine AS go-builder

ENV GOPROXY=https://goproxy.cn,direct

RUN apk add --no-cache g++ musl-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o server ./cmd/server
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o judge-worker ./cmd/judge-worker

# Stage 2: Runtime
# Compatible with gVisor (runsc) runtime for kernel-level sandbox isolation.
# No changes needed in the image — gVisor operates at the container runtime layer
# below the OCI image. Set SANDBOX_MODE=gvisor in the container environment
# to use the gVisor-optimized sandbox path (skips unshare/cgroups, keeps
# chroot jail + seccomp for defense-in-depth).
FROM alpine:3.21

RUN apk add --no-cache python3 g++ ca-certificates tzdata && \
    mkdir -p /data/testcases

COPY --from=go-builder /app/server /usr/local/bin/server
COPY --from=go-builder /app/judge-worker /usr/local/bin/judge-worker

# Include frontend dist for standalone serving
COPY --from=frontend-builder /app/dist /usr/local/share/judgex/frontend
ENV FRONTEND_DIST=/usr/local/share/judgex/frontend

EXPOSE 8080

# Default to API server; override with "judge-worker" for judge pods
CMD ["server"]
