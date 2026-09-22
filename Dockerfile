# ============================================================
# 小米摄像头录像合并工具 - 多架构 Dockerfile
# 支持 linux/amd64（Intel/AMD 群晖）与 linux/arm64（ARM 群晖）
# 采用【两阶段构建】：
#   阶段一（builder）：在 golang 镜像中编译出静态 Go 二进制；
#   阶段二（runtime）：在极简 alpine + ffmpeg 镜像中仅放入该二进制，
#   最终镜像体积约 100MB（上游方案约 280MB，且仅支持 amd64）。
# ============================================================

# ---------- 构建阶段：编译 Go 二进制 ----------
FROM golang:1.22-alpine AS builder

WORKDIR /build

# 仅复制构建所需文件（配合 .dockerignore 减小构建上下文）
COPY go.mod ./
COPY lib ./lib
COPY main.go ./

# 编译：
#   - GO111MODULE=on 启用 Go Modules
#   - GOPROXY 使用国内代理加速拉取（无第三方依赖，实际影响很小）
#   - CGO_ENABLED=0 产出纯静态二进制，可在任意 linux 架构上运行
#   - -ldflags="-s -w" 去掉调试符号与符号表，进一步减小体积
RUN go env -w GO111MODULE=on \
    && go env -w GOPROXY=https://goproxy.cn,direct \
    && CGO_ENABLED=0 go build -ldflags="-s -w" -o xiaomi_camera_merge main.go

# ---------- 运行阶段：极简运行时，仅含 ffmpeg ----------
FROM alpine:3.20

# 默认时区与常用环境变量（可在 docker-compose.yml 中覆盖）
ENV TZ=Asia/Shanghai \
    DELETE_SUCCESS= \
    MAX_MERGE= \
    VIDEO_EXT=mp4

# 安装 ffmpeg（合并核心）、时区数据、CA 证书，并设置上海时区
RUN apk add --no-cache ffmpeg tzdata ca-certificates \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && rm -rf /var/cache/apk/*

# 工作目录与视频挂载点
WORKDIR /app

# 预创建 video 目录（部署时挂载 /volume1/CCTV 到 /app/video）
RUN mkdir -p /app/video

# 从构建阶段拷贝编译好的二进制
COPY --from=builder /build/xiaomi_camera_merge /app/xiaomi_camera_merge

# 容器启动即执行合并（一次性任务，运行完自动退出；定时由群晖任务计划触发）
CMD ["./xiaomi_camera_merge", "-path", "./video"]
