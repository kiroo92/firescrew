# 卡车方向检测功能 - 部署指南

## 📋 功能说明

此功能可以自动识别卡车是**进入**还是**离开**监控区域，并将方向信息保存到数据库。

## 🎯 已完成的修改

### 1. 代码修改

- ✅ `TrackedObject` 结构体添加方向跟踪字段
- ✅ `Config` 结构体添加方向检测配置
- ✅ `DBMotionSnapshot` 添加 direction 字段
- ✅ 实现 `determineDirection()` 函数计算移动方向
- ✅ 更新 `findObjectPosition()` 函数跟踪物体轨迹
- ✅ 更新数据库插入语句包含方向信息
- ✅ 日志输出包含方向信息

### 2. 配置文件更新

`config-camera2.json` 已更新：

```json
{
  "motion": {
    "confidenceMinThreshold": 0.5,
    "lookForClasses": ["truck"],
    "prebufferSeconds": 5,
    "eventGap": 15
  },
  "pixelMotionAreaThreshold": 100.00,
  "objectCenterMovementThreshold": 150.0,
  "objectAreaThreshold": 5000.0,
  "directionDetection": {
    "enabled": true,
    "entryLine": "",
    "exitLine": "",
    "minMovementPixels": 100.0
  }
}
```

### 3. 数据库迁移

- ✅ 创建迁移脚本: `migrations/add_direction_column.sql`
- ✅ 创建自动化脚本: `scripts/apply_migration.sh`

## 🚀 部署步骤

### 步骤 1: 数据库迁移

```bash
cd /srv/code/firescrew/scripts
chmod +x apply_migration.sh
./apply_migration.sh
```

或手动执行：

```bash
psql -h 10.168.1.102 -U postgres -d smartdbase_prod_videogis \
  -f migrations/add_direction_column.sql
```

### 步骤 2: 编译新版本

```bash
cd /srv/code/firescrew
go build -o firescrew
```

### 步骤 3: 重启服务

如果使用 Docker：

```bash
cd /srv/code/firescrew
docker-compose down
docker-compose up -d --build
```

如果直接运行：

```bash
./firescrew config-camera2.json
```

## 📊 验证功能

### 1. 查看日志

日志应该包含方向信息：

```
TRIGGERED NEW OBJECT @ COORD: (500,300) AREA: 45000.0 [truck|0.85] DIRECTION: entering
```

### 2. 查询数据库

```sql
-- 查看最近的检测记录
SELECT 
    event_id,
    object_class,
    direction,
    motion_start,
    center_x,
    center_y
FROM motion_snapshots
WHERE object_class = 'truck'
ORDER BY motion_start DESC
LIMIT 10;

-- 统计进出数量
SELECT 
    direction,
    COUNT(*) as count
FROM motion_snapshots
WHERE object_class = 'truck'
  AND motion_start > NOW() - INTERVAL '1 day'
GROUP BY direction;
```

## 🎨 方向判断规则

当前默认规则（可根据实际情况修改）：

| 移动方向 | 判断结果 |
|---------|---------|
| 向右 (→) | entering |
| 向下 (↓) | entering |
| 向左 (←) | exiting |
| 向上 (↑) | exiting |

### 自定义方向规则

编辑 `firescrew.go` 中的 `determineDirection` 函数（约 1612 行）：

```go
// 根据你的摄像头位置调整
switch direction {
case "right", "down":
    return "entering"
case "left", "up":
    return "exiting"
default:
    return "unknown"
}
```

## ⚙️ 参数调优

### 针对卡车检测优化的参数

| 参数 | 值 | 说明 |
|-----|-----|-----|
| confidenceMinThreshold | 0.5 | 置信度阈值，减少误报 |
| objectCenterMovementThreshold | 150.0 | 中心点移动阈值（像素） |
| objectAreaThreshold | 5000.0 | 面积变化阈值（平方像素） |
| minMovementPixels | 100.0 | 最小移动距离才判断方向 |
| prebufferSeconds | 5 | 预缓冲时间 |
| eventGap | 15 | 事件间隔时间 |

### 调优建议

**如果方向总是 "unknown"**：
- 降低 `minMovementPixels` 到 50-80

**如果同一卡车被识别为多个物体**：
- 增大 `objectCenterMovementThreshold` 到 200
- 增大 `objectAreaThreshold` 到 8000

**如果不同卡车被识别为同一个**：
- 减小 `objectCenterMovementThreshold` 到 100
- 减小 `objectAreaThreshold` 到 3000

## 📖 更多文档

- 详细说明: `docs/DIRECTION_DETECTION.md`
- 数据库迁移: `migrations/add_direction_column.sql`

## 🐛 故障排查

1. **方向始终为 unknown**
   - 检查 `directionDetection.enabled` 是否为 true
   - 降低 `minMovementPixels` 值
   - 查看日志中物体的坐标变化

2. **数据库插入失败**
   - 确认已执行数据库迁移
   - 检查 direction 列是否存在

3. **方向判断不准确**
   - 根据摄像头位置调整方向映射规则
   - 增加 `minMovementPixels` 确保有足够移动距离

