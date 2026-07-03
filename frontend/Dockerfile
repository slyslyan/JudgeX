# Stage 1: Build frontend
FROM node:22-alpine AS builder

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

WORKDIR /app
COPY package.json package-lock.json* .npmrc* ./
RUN npm ci --registry=https://registry.npmmirror.com
COPY . .
RUN npm run build

# Stage 2: Serve with nginx
FROM nginx:alpine

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
ENTRYPOINT ["nginx", "-g", "daemon off;"]
