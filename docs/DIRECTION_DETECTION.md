# 方向检测功能说明

## 功能概述

方向检测功能可以识别卡车（或其他物体）是**进入**还是**离开**监控区域。

## 工作原理

系统通过跟踪物体的移动轨迹来判断方向：

1. **初始位置记录**：当物体首次被检测到时，记录其初始中心点位置
2. **移动向量计算**：持续跟踪物体，计算相对于初始位置的位移向量
3. **方向判断**：根据移动向量的主要方向（上/下/左/右）判断为进入或离开

## 配置说明

### 在 config.json 中启用方向检测

```json
{
  "directionDetection": {
    "enabled": true,
    "entryLine": "",
    "exitLine": "",
    "minMovementPixels": 100.0
  }
}
```

### 参数说明

- **enabled**: 是否启用方向检测（true/false）
- **minMovementPixels**: 最小移动像素数，低于此值不判断方向（默认 100.0）
- **entryLine**: 预留字段，用于未来定义进入线
- **exitLine**: 预留字段，用于未来定义离开线

### 方向映射规则

当前默认规则（可根据摄像头位置自定义）：

```
向右移动 (right) → entering (进入)
向下移动 (down)  → entering (进入)
向左移动 (left)  → exiting (离开)
向上移动 (up)    → exiting (离开)
```

## 自定义方向规则

根据你的摄像头安装位置，可能需要修改方向映射规则。

编辑 `firescrew.go` 中的 `determineDirection` 函数：

```go
// 示例：如果摄像头在入口右侧，卡车从左向右进入
switch direction {
case "right", "down":
    return "entering"
case "left", "up":
    return "exiting"
default:
    return "unknown"
}
```

## 数据库字段

方向信息会保存到 `motion_snapshots` 表的 `direction` 字段：

- `"entering"`: 进入
- `"exiting"`: 离开  
- `"unknown"`: 未知（移动距离不足或刚检测到）

## 数据库迁移

如果你的数据库已经存在，需要运行迁移脚本添加 direction 列：

```bash
psql -h 10.168.1.102 -U postgres -d smartdbase_prod_videogis -f migrations/add_direction_column.sql
```

## 查询示例

### 查询所有进入的卡车

```sql
SELECT * FROM motion_snapshots 
WHERE object_class = 'truck' 
AND direction = 'entering'
ORDER BY motion_start DESC;
```

### 统计进入和离开的数量

```sql
SELECT 
    direction,
    COUNT(*) as count
FROM motion_snapshots
WHERE object_class = 'truck'
GROUP BY direction;
```

### 按日期统计进出

```sql
SELECT 
    DATE(motion_start) as date,
    direction,
    COUNT(*) as count
FROM motion_snapshots
WHERE object_class = 'truck'
GROUP BY DATE(motion_start), direction
ORDER BY date DESC, direction;
```

## 日志输出

启用方向检测后，日志会包含方向信息：

```
TRIGGERED NEW OBJECT @ COORD: (500,300) AREA: 45000.0 [truck|0.85] DIRECTION: entering
```

## 调试建议

1. **检查 minMovementPixels**：如果方向总是 "unknown"，可能是移动距离不足，降低此值
2. **观察日志**：查看物体的坐标变化，确认移动方向是否符合预期
3. **调整阈值**：根据实际情况调整 `objectCenterMovementThreshold` 和 `objectAreaThreshold`

## 注意事项

- 方向判断需要物体移动一定距离才准确
- 快速通过的物体可能来不及判断方向
- 遮挡或重叠的物体可能影响方向判断准确性

