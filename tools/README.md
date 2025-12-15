# Firescrew 工具集

这个目录包含了用于配置和管理 Firescrew 的辅助工具。

## IgnoreAreasClasses 区域编辑器

一个可视化工具，用于快速绘制和配置检测忽略区域。

### 功能特性

- ✅ 支持从视频流截图或本地图片加载
- ✅ 矩形和多边形两种绘制模式
- ✅ 可视化选择要忽略的检测类别
- ✅ 支持自定义检测类别
- ✅ 实时预览已绘制区域
- ✅ 自动生成 JSON 配置
- ✅ 一键复制到剪贴板

### 使用方法

#### 方式1: 使用 Go 服务器（推荐）

```bash
# 编译并运行
cd tools
go run ignore-area-server.go

# 或指定端口
go run ignore-area-server.go -port 8080

# 在浏览器中打开
# http://localhost:8080
```

#### 方式2: 直接打开 HTML 文件

```bash
# 使用浏览器直接打开
firefox ignore-area-editor.html
# 或
google-chrome ignore-area-editor.html
```

### 绘制步骤

1. **加载图片**
   - 输入视频流地址（如 `http://localhost:8042`）并点击"加载视频流截图"
   - 或上传本地图片文件

2. **选择绘制模式**
   - **矩形模式**: 点击并拖拽绘制矩形区域
   - **多边形模式**: 点击添加顶点，双击完成绘制

3. **选择检测类别**
   - 点击常用类别（如 person, car, truck）
   - 或输入自定义类别名称

4. **完成区域**
   - 点击"完成当前区域"保存
   - 可继续绘制多个区域

5. **复制配置**
   - 点击"📋 复制到剪贴板"
   - 粘贴到配置文件的 `ignoreAreasClasses` 字段

### ignoreAreasClasses 格式说明

```json
{
  "ignoreAreasClasses": [
    {
      "class": ["person", "car"],     // 要在此区域忽略的检测类别（数组）
      "coordinates": "100,500,200,800"  // 格式：top,bottom,left,right (4个整数)
    }
  ]
}
```

#### 字段说明

- **class**: 字符串数组，指定在该区域内要忽略的检测类别
- **coordinates**: 字符串，格式为 **"top,bottom,left,right"**（4个逗号分隔的整数）
  - **top**: 矩形区域上边界的 Y 坐标
  - **bottom**: 矩形区域下边界的 Y 坐标
  - **left**: 矩形区域左边界的 X 坐标
  - **right**: 矩形区域右边界的 X 坐标

**注意**: coordinates 字段会在程序启动时被解析为 top, bottom, left, right 四个整数字段。

### 常用检测类别

COCO 数据集常用类别：
- 人物: `person`
- 车辆: `car`, `truck`, `bus`, `motorcycle`, `bicycle`
- 动物: `dog`, `cat`, `bird`
- 物品: `chair`, `bottle`, `cup`

### 示例配置

```json
{
  "ignoreAreasClasses": [
    {
      "class": ["person"],
      "coordinates": "0,1080,0,100"
    },
    {
      "class": ["car", "truck"],
      "coordinates": "0,200,1820,1920"
    }
  ]
}
```

上述配置定义了两个忽略区域：
1. 左侧区域（x: 0-100）忽略人物检测
2. 上方区域（y: 0-200, x: 1820-1920）忽略车辆检测

### 注意事项

1. **坐标系统**: 左上角为原点 (0,0)，向右为 x 轴正方向，向下为 y 轴正方向
2. **矩形绘制**: 只支持矩形区域（拖拽绘制）
3. **坐标格式**: coordinates 必须是 "top,bottom,left,right" 格式（4个整数）
4. **视频流**: 确保视频流服务正在运行且可访问
5. **CORS问题**: 如果直接打开 HTML 遇到跨域问题，请使用 Go 服务器方式

### 编译独立可执行文件

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o ignore-area-editor-linux ignore-area-server.go

# Windows
GOOS=windows GOARCH=amd64 go build -o ignore-area-editor.exe ignore-area-server.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o ignore-area-editor-macos ignore-area-server.go
```

### 技术栈

- 纯 HTML + CSS + JavaScript (无依赖)
- Canvas API 用于图形绘制
- Go embed 文件系统
- 标准库 HTTP 服务器
