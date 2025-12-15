#!/bin/bash

# IgnoreAreasClasses 区域编辑器启动脚本

PORT=${1:-8090}

echo "🔨 检查是否需要编译..."
if [ ! -f "ignore-area-editor" ] || [ "ignore-area-server.go" -nt "ignore-area-editor" ]; then
    echo "📦 正在编译..."
    go build -o ignore-area-editor ignore-area-server.go
    if [ $? -ne 0 ]; then
        echo "❌ 编译失败"
        exit 1
    fi
    echo "✅ 编译完成"
fi

echo ""
echo "🚀 启动 IgnoreAreasClasses 区域编辑器..."
echo "📍 访问地址: http://localhost:$PORT"
echo "💡 提示: 按 Ctrl+C 停止服务"
echo ""

./ignore-area-editor -port $PORT
