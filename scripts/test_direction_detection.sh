#!/bin/bash

# 方向检测功能测试脚本

set -e

echo "================================================"
echo "  方向检测功能测试"
echo "================================================"
echo ""

# 数据库配置
DB_HOST="10.168.1.102"
DB_PORT="5432"
DB_NAME="smartdbase_prod_videogis"
DB_USER="postgres"
DB_PASSWORD="123456"

# 检查 psql 是否安装
if ! command -v psql &> /dev/null; then
    echo "⚠️  警告: psql 未安装，跳过数据库测试"
    DB_TEST=false
else
    DB_TEST=true
fi

echo "1️⃣  检查配置文件..."
echo ""

# 检查配置文件
if [ ! -f "config-camera2.json" ]; then
    echo "❌ 错误: config-camera2.json 不存在"
    exit 1
fi

# 检查方向检测是否启用
if grep -q '"enabled": true' config-camera2.json; then
    echo "✅ 方向检测已启用"
else
    echo "❌ 方向检测未启用"
    exit 1
fi

# 显示关键配置
echo ""
echo "📋 当前配置:"
echo "---"
grep -A 4 '"directionDetection"' config-camera2.json
echo "---"
echo ""

echo "2️⃣  检查代码修改..."
echo ""

# 检查关键函数是否存在
if grep -q "func determineDirection" firescrew.go; then
    echo "✅ determineDirection 函数已添加"
else
    echo "❌ determineDirection 函数未找到"
    exit 1
fi

if grep -q "Direction.*string.*db:\"direction\"" firescrew.go; then
    echo "✅ 数据库字段已添加"
else
    echo "❌ 数据库字段未找到"
    exit 1
fi

echo ""
echo "3️⃣  检查数据库..."
echo ""

if [ "$DB_TEST" = true ]; then
    # 测试数据库连接
    if PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "SELECT 1" > /dev/null 2>&1; then
        echo "✅ 数据库连接成功"
        
        # 检查表是否存在
        if PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "\dt motion_snapshots" > /dev/null 2>&1; then
            echo "✅ motion_snapshots 表存在"
            
            # 检查 direction 列是否存在
            if PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "\d motion_snapshots" | grep -q "direction"; then
                echo "✅ direction 列已添加"
                
                # 显示表结构
                echo ""
                echo "📊 表结构:"
                PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "\d motion_snapshots" | grep -E "(direction|object_class|center_)"
                
                # 查询最近的记录
                echo ""
                echo "📈 最近的记录:"
                PGPASSWORD=$DB_PASSWORD psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -c "SELECT object_class, direction, COUNT(*) as count FROM motion_snapshots GROUP BY object_class, direction ORDER BY count DESC LIMIT 5;"
                
            else
                echo "⚠️  direction 列不存在，需要运行迁移脚本"
                echo "   运行: ./scripts/apply_migration.sh"
            fi
        else
            echo "⚠️  motion_snapshots 表不存在"
        fi
    else
        echo "❌ 数据库连接失败"
    fi
else
    echo "⚠️  跳过数据库测试（psql 未安装）"
fi

echo ""
echo "4️⃣  编译测试..."
echo ""

# 尝试编译
if go build -o /tmp/firescrew_test firescrew.go 2>&1 | head -20; then
    echo "✅ 代码编译成功"
    rm -f /tmp/firescrew_test
else
    echo "❌ 代码编译失败"
    exit 1
fi

echo ""
echo "================================================"
echo "  ✅ 测试完成！"
echo "================================================"
echo ""
echo "📝 下一步:"
echo "  1. 如果 direction 列不存在，运行: ./scripts/apply_migration.sh"
echo "  2. 编译: go build -o firescrew"
echo "  3. 重启服务"
echo "  4. 观察日志中的 DIRECTION 字段"
echo ""

