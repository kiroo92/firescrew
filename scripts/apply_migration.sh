#!/bin/bash

# 数据库迁移脚本 - 添加 direction 列
# 用法: ./apply_migration.sh

set -e

# 数据库配置（从 config-camera2.json 读取）
DB_HOST="10.168.1.102"
DB_PORT="5432"
DB_NAME="smartdbase_prod_videogis"
DB_USER="postgres"
DB_PASSWORD="123456"

echo "================================================"
echo "  数据库迁移：添加 direction 列"
echo "================================================"
echo ""
echo "数据库信息:"
echo "  Host: $DB_HOST"
echo "  Port: $DB_PORT"
echo "  Database: $DB_NAME"
echo "  User: $DB_USER"
echo ""

# 检查 psql 是否安装
if ! command -v psql &> /dev/null; then
    echo "❌ 错误: psql 未安装"
    echo "请安装 PostgreSQL 客户端:"
    echo "  Ubuntu/Debian: sudo apt-get install postgresql-client"
    echo "  CentOS/RHEL: sudo yum install postgresql"
    echo "  macOS: brew install postgresql"
    exit 1
fi

# 检查迁移文件是否存在
MIGRATION_FILE="../migrations/add_direction_column.sql"
if [ ! -f "$MIGRATION_FILE" ]; then
    echo "❌ 错误: 迁移文件不存在: $MIGRATION_FILE"
    exit 1
fi

echo "📋 迁移文件: $MIGRATION_FILE"
echo ""
echo "⚠️  警告: 此操作将修改数据库表结构"
read -p "是否继续? (y/N): " -n 1 -r
echo ""

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "❌ 已取消"
    exit 0
fi

echo ""
echo "🚀 开始执行迁移..."
echo ""

# 执行迁移
PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -f $MIGRATION_FILE

if [ $? -eq 0 ]; then
    echo ""
    echo "✅ 迁移成功完成！"
    echo ""
    echo "验证结果:"
    PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "\d motion_snapshots" | grep direction
    echo ""
    echo "📊 查询示例:"
    echo "  SELECT direction, COUNT(*) FROM motion_snapshots GROUP BY direction;"
else
    echo ""
    echo "❌ 迁移失败"
    exit 1
fi

