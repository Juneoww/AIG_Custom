# 多阶段构建Dockerfile
# 第一阶段：构建Go应用
FROM golang:1.23.2-alpine AS builder

# 仅构建阶段使用，可通过 --build-arg 覆盖；避免依赖下载受默认代理瞬时故障影响。
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

# 设置工作目录
WORKDIR /app

# 复制源代码（包含go.mod和go.sum）
COPY . .

# 下载依赖
RUN go mod download

# 构建应用
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -buildvcs=false -o ai-infra-guard ./cmd/cli/main.go

# 数据库迁移只需要平台二进制，无需安装 Web 运行时的 Python 依赖。
FROM builder AS migrate
WORKDIR /app
ENTRYPOINT ["/app/ai-infra-guard"]

# 第二阶段：运行阶段（使用Python 3.12 Alpine镜像）
FROM python:3.12-alpine

# 安装运行时依赖
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    bash \
    curl \
    git

# 安装uv到/usr/local/bin
RUN curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR="/usr/local/bin" sh

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件和配置文件
COPY --from=builder /app/ai-infra-guard .
COPY --from=builder /app/trpc_go.yaml .
COPY --from=builder /app/CHANGELOG.md .
COPY --from=builder /app/LICENSE /app/licenses/LICENSE
COPY --from=builder /app/internal/platform/reports/assets/DROID_FONT_LICENSE.txt /app/licenses/DROID_FONT_LICENSE.txt
COPY --from=builder /app/internal/platform/reports/assets/THIRD_PARTY_NOTICES.txt /app/licenses/THIRD_PARTY_NOTICES.txt

# 复制数据文件到容器中
COPY --from=builder /app/data ./data

# 复制agent-scan目录并安装Python依赖
COPY ./agent-scan /app/agent-scan
RUN pip install --no-cache-dir -r /app/agent-scan/requirements.txt

# 复制启动脚本到镜像中
COPY start.sh /app/start.sh
RUN chmod +x /app/start.sh && chown root:root /app/start.sh

# 创建必要的目录并设置权限（仅对镜像内有效）
RUN mkdir -p /app/uploads \
    chown -R root:root /app && \
    chmod -R 755 /app && \
    mkdir -p /app/AIG-PromptSecurity/utils
COPY ./AIG-PromptSecurity/utils/strategy_map.json /app/AIG-PromptSecurity/utils/strategy_map.json

# 设置环境变量
ENV APP_ENV=production
ENV UPLOAD_DIR=/app/uploads
ENV DB_DRIVER=postgres
ENV TZ=Asia/Shanghai
ENV PYTHONUNBUFFERED=1

# 暴露端口
EXPOSE 8088

# 声明卷挂载点
VOLUME ["/app/uploads", "/app/data", "/app/logs"]

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD pgrep ai-infra-guard || exit 1

# 启动命令
CMD ["/app/start.sh"]
