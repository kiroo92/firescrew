# 🚀 卡车方向检测 - 快速开始

## ✅ 已完成的工作

### 1. 参数优化（针对卡车检测）

| 参数 | 原值 | 新值 | 说明 |
|-----|------|------|------|
| lookForClasses | ["car","truck"] | ["truck"] | 只检测卡车 |
| confidenceMinThreshold | 0.3 | 0.5 | 提高置信度，减少误报 |
| objectCenterMovementThreshold | 50.0 | 150.0 | 适应卡车大幅移动 |
| objectAreaThreshold | 2000.0 | 5000.0 | 适应卡车面积变化 |
| pixelMotionAreaThreshold | 50.0 | 100.0 | 过滤小物体干扰 |
| prebufferSeconds | 3 | 5 | 捕获更完整进入过程 |
| eventGap | 10 | 15 | 避免同一卡车分割成多个事件 |

### 2. 方向检测功能

- ✅ 自动识别卡车是**进入**还是**离开**
- ✅ 跟踪物体移动轨迹
- ✅ 保存方向信息到数据库
- ✅ 日志输出包含方向

## 📦 部署步骤

### 步骤 1: 数据库迁移（添加 direction 列）

```bash
cd /srv/code/firescrew
./scripts/apply_migration.sh
```

### 步骤 2: 编译

```bash
go build -o firescrew
```

### 步骤 3: 测试配置

```bash
./scripts/test_direction_detection.sh
```

### 步骤 4: 重启服务

**使用 Docker:**
```bash
docker-compose down
docker-compose up -d --build
```

**直接运行:**
```bash
./firescrew config-camera2.json
```

## 📊 验证功能

### 1. 查看日志

日志应该显示方向信息：

```
TRIGGERED NEW OBJECT @ COORD: (500,300) AREA: 45000.0 [truck|0.85] DIRECTION: entering
```

### 2. 查询数据库

```sql
-- 查看最近检测到的卡车
SELECT 
    event_id,
    direction,
    motion_start,
    center_x,
    center_y
FROM motion_snapshots
WHERE object_class = 'truck'
ORDER BY motion_start DESC
LIMIT 10;

-- 统计今天的进出数量
SELECT 
    direction,
    COUNT(*) as count
FROM motion_snapshots
WHERE object_class = 'truck'
  AND DATE(motion_start) = CURRENT_DATE
GROUP BY direction;
```

## 🎯 方向判断规则

当前默认规则：

```
向右移动 → entering (进入)
向下移动 → entering (进入)
向左移动 → exiting (离开)
向上移动 → exiting (离开)
```

### 自定义方向规则

根据你的摄像头位置，编辑 `firescrew.go` 的 `determineDirection` 函数（约 1650 行）：

```go
// 示例：如果卡车从上往下是进入
switch direction {
case "down", "right":
    return "entering"
case "up", "left":
    return "exiting"
default:
    return "unknown"
}
```

## ⚙️ 参数微调

### 如果方向总是 "unknown"

```json
{
  "directionDetection": {
    "minMovementPixels": 50.0  // 降低到 50
  }
}
```

### 如果同一卡车被识别为多个物体

```json
{
  "objectCenterMovementThreshold": 200.0,  // 增加到 200
  "objectAreaThreshold": 8000.0            // 增加到 8000
}
```

### 如果不同卡车被识别为同一个

```json
{
  "objectCenterMovementThreshold": 100.0,  // 减少到 100
  "objectAreaThreshold": 3000.0            // 减少到 3000
}
```

## 📁 文件说明

| 文件 | 说明 |
|------|------|
| `config-camera2.json` | 主配置文件（已优化） |
| `firescrew.go` | 主程序（已添加方向检测） |
| `migrations/add_direction_column.sql` | 数据库迁移脚本 |
| `scripts/apply_migration.sh` | 自动化迁移脚本 |
| `scripts/test_direction_detection.sh` | 功能测试脚本 |
| `docs/DIRECTION_DETECTION.md` | 详细文档 |
| `DIRECTION_DETECTION_SETUP.md` | 部署指南 |

## 🐛 常见问题

### Q: 方向始终为 "unknown"
**A:** 
1. 检查 `directionDetection.enabled` 是否为 true
2. 降低 `minMovementPixels` 到 50
3. 确保卡车有足够的移动距离

### Q: 数据库插入失败
**A:**
1. 确认已运行数据库迁移脚本
2. 检查 direction 列是否存在：
   ```sql
   \d motion_snapshots
   ```

### Q: 方向判断不准确
**A:**
1. 观察日志中的坐标变化
2. 根据摄像头位置调整方向映射规则
3. 增加 `minMovementPixels` 确保有足够移动距离

## 📞 技术支持

- 详细文档: `docs/DIRECTION_DETECTION.md`
- 部署指南: `DIRECTION_DETECTION_SETUP.md`
- 测试脚本: `./scripts/test_direction_detection.sh`

## 🎉 完成！

现在系统可以：
- ✅ 只检测卡车（过滤其他车辆）
- ✅ 更准确地跟踪移动的卡车
- ✅ 自动识别进入/离开方向
- ✅ 保存方向信息到数据库
- ✅ 减少误报和重复检测

