# 方向检测配置示例

## 场景 1: 水平道路（左右移动）

```
摄像头视角：
┌─────────────────────────────────────┐
│                                     │
│  ←─────── exiting                   │
│                                     │
│           [摄像头]                  │
│                                     │
│  entering ───────→                  │
│                                     │
└─────────────────────────────────────┘
```

**配置代码:**
```go
switch direction {
case "right":
    return "entering"
case "left":
    return "exiting"
default:
    return "unknown"
}
```

**配置参数:**
```json
{
  "directionDetection": {
    "enabled": true,
    "minMovementPixels": 100.0
  },
  "objectCenterMovementThreshold": 150.0
}
```

---

## 场景 2: 垂直道路（上下移动）

```
摄像头视角：
┌─────────────────────────────────────┐
│              ↑                      │
│              │                      │
│           exiting                   │
│              │                      │
│         [摄像头]                    │
│              │                      │
│          entering                   │
│              │                      │
│              ↓                      │
└─────────────────────────────────────┘
```

**配置代码:**
```go
switch direction {
case "down":
    return "entering"
case "up":
    return "exiting"
default:
    return "unknown"
}
```

---

## 场景 3: 十字路口（多方向）

```
摄像头视角：
┌─────────────────────────────────────┐
│              ↑                      │
│              │                      │
│    ←─────────┼─────────→           │
│              │                      │
│         [摄像头]                    │
│              │                      │
│              ↓                      │
└─────────────────────────────────────┘
```

**配置代码（进入=向中心，离开=离开中心）:**
```go
// 计算是否靠近中心
centerX := 960  // 1920/2
centerY := 540  // 1080/2

initialDist := math.Sqrt(float64(
    (object.InitialCenter.X-centerX)*(object.InitialCenter.X-centerX) +
    (object.InitialCenter.Y-centerY)*(object.InitialCenter.Y-centerY)))

currentDist := math.Sqrt(float64(
    (object.Center.X-centerX)*(object.Center.X-centerX) +
    (object.Center.Y-centerY)*(object.Center.Y-centerY)))

if currentDist < initialDist {
    return "entering"  // 靠近中心
} else {
    return "exiting"   // 远离中心
}
```

---

## 场景 4: 斜角道路

```
摄像头视角：
┌─────────────────────────────────────┐
│  ↖                            ↗     │
│    exiting              entering    │
│                                     │
│         [摄像头]                    │
│                                     │
│    entering              exiting    │
│  ↙                            ↘     │
└─────────────────────────────────────┘
```

**配置代码（组合判断）:**
```go
absX := math.Abs(float64(object.MovementVector.X))
absY := math.Abs(float64(object.MovementVector.Y))

// 右下方向 = 进入
if object.MovementVector.X > 0 && object.MovementVector.Y > 0 {
    return "entering"
}
// 左上方向 = 离开
if object.MovementVector.X < 0 && object.MovementVector.Y < 0 {
    return "exiting"
}
// 其他方向根据主要轴判断
if absX > absY {
    if object.MovementVector.X > 0 {
        return "entering"
    } else {
        return "exiting"
    }
} else {
    if object.MovementVector.Y > 0 {
        return "entering"
    } else {
        return "exiting"
    }
}
```

---

## 实际调试步骤

### 1. 启用调试日志

在 `findObjectPosition` 函数中取消注释日志：

```go
Log("warning", fmt.Sprintf("UPDATING OBJECT @ %d|%f TO %d|%f DISTANCE: %d ADIFF: %d DIRECTION: %s", 
    lastPositions[i].Center, lastPositions[i].Area, 
    object.Center, object.Area, 
    int(distance), int(areaDiff), object.Direction))
```

### 2. 观察坐标变化

运行系统并观察日志输出：

```
UPDATING OBJECT @ (500,300)|45000 TO (650,320)|46000 DISTANCE: 150 ADIFF: 1000 DIRECTION: entering
UPDATING OBJECT @ (650,320)|46000 TO (800,340)|47000 DISTANCE: 150 ADIFF: 1000 DIRECTION: entering
```

从日志可以看出：
- X 坐标从 500 → 650 → 800（向右移动）
- Y 坐标从 300 → 320 → 340（向下移动）
- 主要移动方向是向右

### 3. 绘制移动轨迹

```
初始位置: (500, 300)
         ↓
当前位置: (800, 340)

移动向量: X=+300, Y=+40
主要方向: 向右 (right)
```

### 4. 调整配置

根据观察结果调整 `determineDirection` 函数。

---

## 配置建议

### 高速道路（卡车快速通过）

```json
{
  "objectCenterMovementThreshold": 200.0,
  "minMovementPixels": 150.0,
  "eventGap": 20
}
```

### 停车场/慢速区域

```json
{
  "objectCenterMovementThreshold": 100.0,
  "minMovementPixels": 50.0,
  "eventGap": 30
}
```

### 远距离监控

```json
{
  "objectCenterMovementThreshold": 80.0,
  "objectAreaThreshold": 3000.0,
  "minMovementPixels": 40.0
}
```

### 近距离监控

```json
{
  "objectCenterMovementThreshold": 250.0,
  "objectAreaThreshold": 10000.0,
  "minMovementPixels": 150.0
}
```

