package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"mime/multipart"
	_ "net/http/pprof"
	"runtime"

	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	_ "github.com/lib/pq"
	"log"
	"math"
	mathRand "math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/8ff/firescrew/pkg/firescrewServe"
	"github.com/8ff/tuna"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/goki/freetype"
	"github.com/goki/freetype/truetype"

	ob "github.com/8ff/firescrew/pkg/objectPredict"
	"github.com/8ff/prettyTimer"
	"github.com/hybridgroup/mjpeg"
)

var Version string

var gifSliceMutex sync.Mutex
var gifSlice []image.RGBA

//go:embed assets/*
var assetsFs embed.FS

var everyNthFrame = 1         // Process every Nth frame, 1 = every frame
var interenceAvgInterval = 10 // Frames to average inference time over

var stream *mjpeg.Stream

type Prediction struct {
	Object     int       `json:"object"`
	ClassName  string    `json:"class_name"`
	Box        []float32 `json:"box"`
	Top        int       `json:"top"`
	Bottom     int       `json:"bottom"`
	Left       int       `json:"left"`
	Right      int       `json:"right"`
	Confidence float32   `json:"confidence"`
	Took       float64   `json:"took"`
}

type Config struct {
	CameraName                    string            `json:"cameraName"`
	PrintDebug                    bool              `json:"printDebug"`
	DeviceUrl                     string            `json:"deviceUrl"`
	LoStreamParamBypass           StreamParams      `json:"loStreamParamBypass"`
	HiResDeviceUrl                string            `json:"hiResDeviceUrl"`
	HiStreamParamBypass           StreamParams      `json:"hiStreamParamBypass"`
	PixelMotionAreaThreshold      float64           `json:"pixelMotionAreaThreshold"`
	ObjectCenterMovementThreshold float64           `json:"objectCenterMovementThreshold"`
	ObjectAreaThreshold           float64           `json:"objectAreaThreshold"`
	StreamDrawIgnoredAreas        bool              `json:"streamDrawIgnoredAreas"`
	IgnoreAreasClasses            []IgnoreAreaClass `json:"ignoreAreasClasses"`
	EnableOutputStream            bool              `json:"enableOutputStream"`
	OutputStreamAddr              string            `json:"outputStreamAddr"`
	DirectionDetection            struct {
		Enabled           bool    `json:"enabled"`
		EntryLine         string  `json:"entryLine"`         // Format: "x1,y1,x2,y2"
		ExitLine          string  `json:"exitLine"`          // Format: "x1,y1,x2,y2"
		MinMovementPixels float64 `json:"minMovementPixels"` // Minimum movement to determine direction
	} `json:"directionDetection"`
	Motion                        struct {
		OnnxModel                 string   `json:"onnxModel"`
		OnnxEnableCoreMl          bool     `json:"onnxEnableCoreMl"`
		EmbeddedObjectScript      string   `json:"EmbeddedObjectScript"`
		ConfidenceMinThreshold    float64  `json:"confidenceMinThreshold"`
		LookForClasses            []string `json:"lookForClasses"`
		NetworkObjectDetectServer string   `json:"networkObjectDetectServer"`
		EventGap                  int      `json:"eventGap"`
		PrebufferSeconds          int      `json:"prebufferSeconds"`
	} `json:"motion"`
	Video struct {
		HiResPath     string `json:"hiResPath"`
		RecodeTsToMp4 bool   `json:"recodeTsToMp4"`
		OnlyRemuxMp4  bool   `json:"onlyRemuxMp4"`
	} `json:"video"`
	Events struct {
		Mqtt struct {
			Host  string `json:"host"`
			Port  int    `json:"port"`
			User  string `json:"user"`
			Pass  string `json:"pass"`
			Topic string `json:"topic"`
		}
		Slack struct {
			Url string `json:"url"`
		}
		PostgreSQL struct {
			Host     string `json:"host"`
			Port     int    `json:"port"`
			Database string `json:"database"`
			User     string `json:"user"`
			Password string `json:"password"`
			SSLMode  string `json:"sslMode"`
		}
		ScriptPath string `json:"scriptPath"`
		Webhook    string `json:"webhookUrl"`
	} `json:"events"`
	Notifications struct {
		EnablePushoverAlerts bool   `json:"enablePushoverAlerts"`
		PushoverAppToken     string `json:"pushoverAppToken"`
		PushoverUserKey      string `json:"pushoverUserKey"`
	} `json:"notifications"`
}

type StreamParams struct {
	Width  int
	Height int
	FPS    float64
}

type InferenceStats struct {
	Avg float64
	Min float64
	Max float64
}

// TODO ADD MUTEX LOCK
type RuntimeConfig struct {
	MotionTriggeredLast time.Time `json:"motionTriggredLast"`
	MotionTriggered     bool      `json:"motionTriggered"`
	// MotionTriggeredChan chan bool `json:"motionTriggeredChan"`
	// MotionHiRecOn bool `json:"motionHiRecOn"`
	HiResControlChannel   chan RecordMsg
	MotionVideo           VideoMetadata
	MotionMutex           *sync.Mutex
	TextFont              *truetype.Font
	LoResStreamParams     StreamParams
	HiResStreamParams     StreamParams
	objectPredictConn     net.Conn
	InferenceTimingBuffer []InferenceStats
	modelReady            bool
	ObjectPredictClient   *ob.Client
	CodecName             string
}

type IgnoreAreaClass struct {
	Class       []string `json:"class"`
	Coordinates string   `json:"coordinates"`
	Top         int      `json:"top"`
	Bottom      int      `json:"bottom"`
	Left        int      `json:"left"`
	Right       int      `json:"right"`
}

type ControlCommand struct {
	StartRecording bool
	Filename       string
}

type TrackedObject struct {
	ID             string          // Unique identifier for tracking
	BBox           image.Rectangle
	Center         image.Point
	Area           float64
	LastMoved      time.Time
	Class          string
	Confidence     float32
	MaxConfidence  float32         // Highest confidence observed during tracking
	FirstSeen      time.Time
	InitialCenter  image.Point
	Direction      string          // "entering", "exiting", "unknown"
	MovementVector image.Point     // X and Y displacement from initial position
	FrameCount     int             // Number of frames this object has been tracked
}

type VideoMetadata struct {
	ID           string
	MotionStart  time.Time
	MotionEnd    time.Time
	Objects      []TrackedObject
	RecodedToMp4 bool
	Snapshots    []string
	VideoFile    string
	CameraName   string
}

type Event struct {
	Type                string          `json:"type"`
	Timestamp           time.Time       `json:"timestamp"`
	MotionTriggeredLast time.Time       `json:"motion_triggered_last"`
	ID                  string          `json:"id"`
	MotionStart         time.Time       `json:"motion_start"`
	MotionEnd           time.Time       `json:"motion_end"`
	Objects             []TrackedObject `json:"objects"`
	RecodedToMp4        bool            `json:"recoded_to_mp4"`
	Snapshots           []string        `json:"snapshots"`
	VideoFile           string          `json:"video_file"`
	CameraName          string          `json:"camera_name"`
	MetadataPath        string          `json:"metadata_path"`
	PredictedObjects    []Prediction    `json:"predictedObjects"`
}

// 数据库表结构 - 单表设计，每个snapshot一条记录，包含对应的Object详细信息
type DBMotionSnapshot struct {
	SnapshotID     string    `db:"snapshot_id"`    // 使用snapshot文件名中的ID作为主键
	EventID        string    `db:"event_id"`
	MotionStart    time.Time `db:"motion_start"`
	MotionEnd      time.Time `db:"motion_end"`
	CameraName     string    `db:"camera_name"`
	VideoFile      string    `db:"video_file"`
	RecodedToMp4   bool      `db:"recoded_to_mp4"`
	SnapshotFile   string    `db:"snapshot_file"`
	SnapshotIndex  int       `db:"snapshot_index"`
	// Object详细信息
	BBoxMinX       int       `db:"bbox_min_x"`
	BBoxMinY       int       `db:"bbox_min_y"`
	BBoxMaxX       int       `db:"bbox_max_x"`
	BBoxMaxY       int       `db:"bbox_max_y"`
	CenterX        int       `db:"center_x"`
	CenterY        int       `db:"center_y"`
	Area           float64   `db:"area"`
	LastMoved      time.Time `db:"last_moved"`
	ObjectClass    string    `db:"object_class"`
	Confidence     float32   `db:"confidence"`
	Direction      string    `db:"direction"`      // "entering", "exiting", "unknown"
	CreatedAt      time.Time `db:"created_at"`
}

var lastPositions = []TrackedObject{}
var globalConfig Config
var runtimeConfig RuntimeConfig
var db *sql.DB

var predictFrameCounter int

type Frame struct {
	Data [][]byte
	Pts  time.Duration
}

type FrameMsg struct {
	Frame image.Image
	Error string
	// Exited   bool
	ExitCode int
}

type StreamInfo struct {
	Streams []struct {
		Width      int     `json:"width"`
		Height     int     `json:"height"`
		CodecType  string  `json:"codec_type"`
		CodecName  string  `json:"codec_name"`
		RFrameRate float64 `json:"-"`
	} `json:"streams"`
}

// RecordMsg struct to control recording
type RecordMsg struct {
	Record   bool
	Filename string
}

func readConfig(path string) Config {
	// Read the configuration file.
	configFile, err := os.ReadFile(path)
	if err != nil {
		Log("error", fmt.Sprintf("Error reading config file: %v", err))
		os.Exit(1)
	}

	// Parse the configuration file into a Config struct.
	var config Config
	err = json.Unmarshal(configFile, &config)
	if err != nil {
		Log("error", fmt.Sprintf("Error parsing config file: %v", err))
		os.Exit(1)
	}

	// Split the coordinates string into separate integers.
	for i, ignoreAreaClass := range config.IgnoreAreasClasses {
		coords := strings.Split(ignoreAreaClass.Coordinates, ",")
		if len(coords) == 4 {
			config.IgnoreAreasClasses[i].Top, err = strconv.Atoi(coords[0])
			if err != nil {
				Log("error", fmt.Sprintf("Error parsing config file: %v", err))
				os.Exit(1)
			}
			config.IgnoreAreasClasses[i].Bottom, err = strconv.Atoi(coords[1])
			if err != nil {
				Log("error", fmt.Sprintf("Error parsing config file: %v", err))
				os.Exit(1)
			}
			config.IgnoreAreasClasses[i].Left, err = strconv.Atoi(coords[2])
			if err != nil {
				Log("error", fmt.Sprintf("Error parsing config file: %v", err))
				os.Exit(1)
			}
			config.IgnoreAreasClasses[i].Right, err = strconv.Atoi(coords[3])
			if err != nil {
				Log("error", fmt.Sprintf("Error parsing config file: %v", err))
				os.Exit(1)
			}
		} else {
			Log("error", fmt.Sprintf("Error parsing config file: %v", errors.New("coordinates string must contain 4 comma separated integers")))
			os.Exit(1)
		}
	}

	if config.Motion.EmbeddedObjectScript == "" {
		Log("error", fmt.Sprintf("Error parsing config file: %v", errors.New("embeddedObjectScript must be set")))
		os.Exit(1)
	}

	if config.Motion.EmbeddedObjectScript != "objectDetectServerYolo.py" && config.Motion.EmbeddedObjectScript != "objectDetectServerCoral.py" && config.Motion.EmbeddedObjectScript != "objectDetectServerCoreML.py" {
		Log("error", fmt.Sprintf("Error parsing config file: %v", errors.New("embeddedObjectScript must be either objectDetectServerYolo.py or objectDetectServerCoral.py")))
		os.Exit(1)
	}

	// Print the configuration properties.
	Log("info", "******************** CONFIG ********************")
	Log("info", fmt.Sprintf("Print Debug: %t", config.PrintDebug))
	Log("info", fmt.Sprintf("Device URL: %s", config.DeviceUrl))
	Log("info", fmt.Sprintf("Lo-Res Param Bypass: Res: %dx%d FPS: %.2f", config.LoStreamParamBypass.Width, config.LoStreamParamBypass.Height, config.LoStreamParamBypass.FPS))
	Log("info", fmt.Sprintf("Hi-Res Param Bypass: Res: %dx%d FPS: %.2f", config.HiStreamParamBypass.Width, config.HiStreamParamBypass.Height, config.HiStreamParamBypass.FPS))
	Log("info", fmt.Sprintf("Hi-Res Device URL: %s", config.HiResDeviceUrl))
	Log("info", fmt.Sprintf("Video HiResPath: %s", config.Video.HiResPath))
	Log("info", fmt.Sprintf("Video RecodeTsToMp4: %t", config.Video.RecodeTsToMp4))
	Log("info", fmt.Sprintf("Video OnlyRemuxMp4: %t", config.Video.OnlyRemuxMp4))
	Log("info", fmt.Sprintf("Motion OnnxModel: %s", config.Motion.OnnxModel))
	Log("info", fmt.Sprintf("Motion OnnxEnableCoreMl: %t", config.Motion.OnnxEnableCoreMl))
	Log("info", fmt.Sprintf("Motion Embedded Object Script: %s", config.Motion.EmbeddedObjectScript))
	Log("info", fmt.Sprintf("Motion Object Min Threshold: %f", config.Motion.ConfidenceMinThreshold))
	Log("info", fmt.Sprintf("Motion LookForClasses: %v", config.Motion.LookForClasses))
	Log("info", fmt.Sprintf("Motion Network Object Detect Server: %s", config.Motion.NetworkObjectDetectServer))
	Log("info", fmt.Sprintf("Motion PrebufferSeconds: %d", config.Motion.PrebufferSeconds))
	Log("info", fmt.Sprintf("Motion EventGap: %d", config.Motion.EventGap))
	Log("info", fmt.Sprintf("Pixel Motion Area Threshold: %f", config.PixelMotionAreaThreshold))
	Log("info", fmt.Sprintf("Object Center Movement Threshold: %f", config.ObjectCenterMovementThreshold))
	Log("info", fmt.Sprintf("Object Area Threshold: %f", config.ObjectAreaThreshold))
	Log("info", "Ignore Areas Classes:")
	for _, ignoreAreaClass := range config.IgnoreAreasClasses {
		Log("info", fmt.Sprintf("  Class: %v, Coordinates: %s", ignoreAreaClass.Class, ignoreAreaClass.Coordinates))
	}
	Log("info", fmt.Sprintf("Draw Ignored Areas: %t", config.StreamDrawIgnoredAreas))
	Log("info", fmt.Sprintf("Enable Output Stream: %t", config.EnableOutputStream))
	Log("info", fmt.Sprintf("Output Stream Address: %s", config.OutputStreamAddr))
	Log("info", "************* EVENTS CONFIG *************")
	Log("info", fmt.Sprintf("Events MQTT Host: %s", config.Events.Mqtt.Host))
	Log("info", fmt.Sprintf("Events MQTT Port: %d", config.Events.Mqtt.Port))
	Log("info", fmt.Sprintf("Events MQTT Topic: %s", config.Events.Mqtt.Topic))
	Log("info", fmt.Sprintf("Events Slack URL: %s", config.Events.Slack.Url))
	Log("info", fmt.Sprintf("Events PostgreSQL Host: %s", config.Events.PostgreSQL.Host))
	Log("info", fmt.Sprintf("Events PostgreSQL Port: %d", config.Events.PostgreSQL.Port))
	Log("info", fmt.Sprintf("Events PostgreSQL Database: %s", config.Events.PostgreSQL.Database))
	Log("info", fmt.Sprintf("Events PostgreSQL User: %s", config.Events.PostgreSQL.User))
	Log("info", fmt.Sprintf("Events Script Path: %s", config.Events.ScriptPath))
	Log("info", fmt.Sprintf("Events Webhook URL: %s", config.Events.Webhook))
	Log("info", "************************************************")

	// Load font into runtime
	fontBytes, err := assetsFs.ReadFile("assets/fonts/Changes.ttf")
	if err != nil {
		Log("error", fmt.Sprintf("Error reading font file: %v", err))
		os.Exit(1)
	}

	font, err := freetype.ParseFont(fontBytes)
	if err != nil {
		Log("error", fmt.Sprintf("Error parsing font file: %v", err))
		os.Exit(1)
	}

	runtimeConfig.TextFont = font

	// Check if pushover tokens are provided if enabled
	if config.Notifications.EnablePushoverAlerts {
		if config.Notifications.PushoverAppToken == "" {
			Log("error", fmt.Sprintf("Error parsing config file: %v", errors.New("pushoverAppToken must be set")))
			os.Exit(1)
		}

		if config.Notifications.PushoverUserKey == "" {
			Log("error", fmt.Sprintf("Error parsing config file: %v", errors.New("pushoverUserKey must be set")))
			os.Exit(1)
		}
	}

	return config
}

// 初始化数据库连接
func initDB() error {
	if globalConfig.Events.PostgreSQL.Host == "" {
		return nil // 如果没有配置PostgreSQL，则跳过
	}

	var err error
	sslMode := globalConfig.Events.PostgreSQL.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		globalConfig.Events.PostgreSQL.Host,
		globalConfig.Events.PostgreSQL.Port,
		globalConfig.Events.PostgreSQL.User,
		globalConfig.Events.PostgreSQL.Password,
		globalConfig.Events.PostgreSQL.Database,
		sslMode)

	db, err = sql.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("无法连接到PostgreSQL: %v", err)
	}

	err = db.Ping()
	if err != nil {
		return fmt.Errorf("无法连接到PostgreSQL数据库: %v", err)
	}

	Log("info", "PostgreSQL数据库连接成功")

	// 创建数据库表
	// err = createTables()
	// if err != nil {
	// 	return fmt.Errorf("创建数据库表失败: %v", err)
	// }

	return nil
}

// 创建数据库表
func createTables() error {
	// 先删除旧表（如果存在）以支持新结构
	dropTable := `DROP TABLE IF EXISTS motion_snapshots;`
	_, err := db.Exec(dropTable)
	if err != nil {
		Log("warning", fmt.Sprintf("删除旧表失败: %v", err))
	}

	// 创建motion_snapshots表 - 单表设计，每个snapshot一条记录，包含Object详细信息
	createTable := `
	CREATE TABLE IF NOT EXISTS motion_snapshots (
		snapshot_id VARCHAR(50) PRIMARY KEY,
		event_id VARCHAR(50) NOT NULL,
		motion_start TIMESTAMP NOT NULL,
		motion_end TIMESTAMP,
		camera_name VARCHAR(255) NOT NULL,
		video_file VARCHAR(255),
		recoded_to_mp4 BOOLEAN DEFAULT FALSE,
		snapshot_file VARCHAR(255) NOT NULL,
		snapshot_index INTEGER NOT NULL,
		bbox_min_x INTEGER,
		bbox_min_y INTEGER,
		bbox_max_x INTEGER,
		bbox_max_y INTEGER,
		center_x INTEGER,
		center_y INTEGER,
		area DOUBLE PRECISION,
		last_moved TIMESTAMP,
		object_class VARCHAR(50),
		confidence REAL,
		direction VARCHAR(20) DEFAULT 'unknown',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`

	_, err = db.Exec(createTable)
	if err != nil {
		return fmt.Errorf("创建motion_snapshots表失败: %v", err)
	}

	// 创建索引以优化查询性能
	createIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_motion_snapshots_event_id ON motion_snapshots(event_id);`,
		`CREATE INDEX IF NOT EXISTS idx_motion_snapshots_motion_start ON motion_snapshots(motion_start);`,
		`CREATE INDEX IF NOT EXISTS idx_motion_snapshots_camera_name ON motion_snapshots(camera_name);`,
		`CREATE INDEX IF NOT EXISTS idx_motion_snapshots_object_class ON motion_snapshots(object_class);`,
	}

	for _, indexSQL := range createIndexes {
		_, err = db.Exec(indexSQL)
		if err != nil {
			Log("warning", fmt.Sprintf("创建索引失败: %v", err))
		}
	}

	Log("info", "数据库表和索引创建成功")
	return nil
}

// 生成UUID
func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// 如果UUID生成失败，使用时间戳+随机数作为备用
		return fmt.Sprintf("%d_%d", time.Now().UnixNano(), mathRand.Intn(10000))
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// 从snapshot文件名中提取完整的snapshot ID
// 例如: snap_hbJL0V2dIgiH5oJ_eohA.jpg -> snap_hbJL0V2dIgiH5oJ_eohA
func extractSnapshotID(snapshotFile string) string {
	// 移除文件扩展名，保留完整的文件名作为ID
	name := strings.TrimSuffix(snapshotFile, filepath.Ext(snapshotFile))
	
	// 验证格式是否正确（应该以snap_开头）
	if strings.HasPrefix(name, "snap_") && len(name) > 5 {
		return name // 返回完整的snapshot名称
	}
	
	// 如果格式不正确，生成一个UUID
	Log("warning", fmt.Sprintf("Snapshot文件名格式不正确: %s，使用UUID", snapshotFile))
	return generateUUID()
}

// 保存事件到数据库
func saveEventToDB(eventData VideoMetadata) error {
	if db == nil {
		return nil // 如果没有数据库连接，则跳过
	}

	// 去重：按物体ID分组，每个唯一物体只保存一条记录
	// 使用最后一次检测的数据（包含最终方向和最高置信度）
	uniqueObjects := make(map[string]struct {
		Object   TrackedObject
		Snapshot string
		Index    int
	})

	// 确保Objects和Snapshots数量匹配
	count := len(eventData.Snapshots)
	if len(eventData.Objects) < count {
		count = len(eventData.Objects)
	}

	for i := 0; i < count; i++ {
		object := eventData.Objects[i]
		snapshot := eventData.Snapshots[i]

		// 使用物体ID作为去重键，如果ID为空则使用索引
		key := object.ID
		if key == "" {
			key = fmt.Sprintf("obj_%d", i)
		}

		// 保留最后一次检测的数据（通常包含最终方向）
		// 或者如果当前置信度更高，也更新
		if existing, exists := uniqueObjects[key]; exists {
			// 保留置信度更高的，或者方向已确定的
			if object.MaxConfidence > existing.Object.MaxConfidence ||
				(object.Direction != "unknown" && existing.Object.Direction == "unknown") {
				uniqueObjects[key] = struct {
					Object   TrackedObject
					Snapshot string
					Index    int
				}{object, snapshot, i}
			}
		} else {
			uniqueObjects[key] = struct {
				Object   TrackedObject
				Snapshot string
				Index    int
			}{object, snapshot, i}
		}
	}

	Log("info", fmt.Sprintf("去重后：从 %d 条记录合并为 %d 条唯一物体记录", count, len(uniqueObjects)))

	// 先删除该事件的旧记录（如果存在）
	deleteSQL := `DELETE FROM motion_snapshots WHERE event_id = $1`
	_, err := db.Exec(deleteSQL, eventData.ID)
	if err != nil {
		return fmt.Errorf("删除旧记录失败: %v", err)
	}

	// 为每个唯一物体插入一条记录
	insertSQL := `
	INSERT INTO motion_snapshots (
		snapshot_id, event_id, motion_start, motion_end, camera_name,
		video_file, recoded_to_mp4, snapshot_file, snapshot_index,
		bbox_min_x, bbox_min_y, bbox_max_x, bbox_max_y,
		center_x, center_y, area, last_moved, object_class, confidence, direction
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`

	insertedCount := 0
	for objectID, data := range uniqueObjects {
		object := data.Object
		snapshot := data.Snapshot

		// 使用物体ID作为snapshot_id（确保唯一性）
		snapshotID := fmt.Sprintf("%s_%s", eventData.ID, objectID)

		// 使用MaxConfidence如果可用，否则使用当前Confidence
		confidence := object.Confidence
		if object.MaxConfidence > confidence {
			confidence = object.MaxConfidence
		}

		_, err = db.Exec(insertSQL,
			snapshotID,                 // snapshot_id
			eventData.ID,               // event_id
			eventData.MotionStart,      // motion_start
			eventData.MotionEnd,        // motion_end
			eventData.CameraName,       // camera_name
			eventData.VideoFile,        // video_file
			eventData.RecodedToMp4,     // recoded_to_mp4
			snapshot,                   // snapshot_file
			data.Index,                 // snapshot_index
			object.BBox.Min.X,          // bbox_min_x
			object.BBox.Min.Y,          // bbox_min_y
			object.BBox.Max.X,          // bbox_max_x
			object.BBox.Max.Y,          // bbox_max_y
			object.Center.X,            // center_x
			object.Center.Y,            // center_y
			object.Area,                // area
			object.LastMoved,           // last_moved
			object.Class,               // object_class
			confidence,                 // confidence (使用最高置信度)
			object.Direction)           // direction (使用最终方向)

		if err != nil {
			return fmt.Errorf("插入snapshot记录失败 (objectID %s): %v", objectID, err)
		}
		insertedCount++
	}

	Log("info", fmt.Sprintf("事件 %s 已保存到数据库，包含 %d 条唯一物体记录", eventData.ID, insertedCount))
	return nil
}

// 关闭数据库连接
func closeDB() {
	if db != nil {
		db.Close()
		Log("info", "数据库连接已关闭")
	}
}

func eventHandler(eventType string, payload []byte) {
	// Log the event type
	// Log("event", fmt.Sprintf("Event: %s", eventType))

	//过滤不是motion_end
	if eventType != "motion_end" {
		return
	}

	// 解析payload为Event结构
	var event Event
	err := json.Unmarshal(payload, &event)
	if err != nil {
		Log("error", fmt.Sprintf("解析事件payload失败: %v", err))
	} else {
		// 转换为VideoMetadata格式并保存到数据库
		videoMetadata := VideoMetadata{
			ID:           event.ID,
			MotionStart:  event.MotionStart,
			MotionEnd:    event.MotionEnd,
			Objects:      event.Objects,
			RecodedToMp4: event.RecodedToMp4,
			Snapshots:    event.Snapshots,
			VideoFile:    event.VideoFile,
			CameraName:   event.CameraName,
		}

		// 保存到PostgreSQL数据库
		err = saveEventToDB(videoMetadata)
		if err != nil {
			Log("error", fmt.Sprintf("保存事件到数据库失败: %v", err))
		}
	}

	// Webhook URL
	if globalConfig.Events.Webhook != "" {
		resp, err := http.Post(globalConfig.Events.Webhook, "application/json", bytes.NewReader(payload))
		if err != nil {
			Log("error", fmt.Sprintf("Failed to post to webhook: %s", err))
		} else {
			defer resp.Body.Close()
		}
	}

	// Script Path
	if globalConfig.Events.ScriptPath != "" {
		cmd := exec.Command(globalConfig.Events.ScriptPath)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			Log("error", fmt.Sprintf("Failed to get stdin pipe: %s", err))
			return
		}

		go func() {
			defer stdin.Close()
			_, err := stdin.Write(payload)
			if err != nil {
				Log("error", fmt.Sprintf("Failed to write to stdin: %s", err))
			}
		}()

		if err := cmd.Start(); err != nil {
			Log("error", fmt.Sprintf("Failed to start script: %s", err))
		}
	}

	// Send to Slack
	if globalConfig.Events.Slack.Url != "" {
		slackMessage := map[string]interface{}{
			"text": fmt.Sprintf("Event: %s\nPayload: %s", eventType, string(payload)),
		}
		slackPayload, _ := json.Marshal(slackMessage)
		resp, err := http.Post(globalConfig.Events.Slack.Url, "application/json", bytes.NewReader(slackPayload))
		if err != nil {
			Log("error", fmt.Sprintf("Failed to post to Slack: %s", err))
		} else {
			defer resp.Body.Close()
		}
	}

	// Send to MQTT
	if globalConfig.Events.Mqtt.Host != "" && globalConfig.Events.Mqtt.Port != 0 && globalConfig.Events.Mqtt.Topic != "" {
		err := sendToMQTT(globalConfig.Events.Mqtt.Topic, string(payload), globalConfig.Events.Mqtt.Host, globalConfig.Events.Mqtt.Port, globalConfig.Events.Mqtt.User, globalConfig.Events.Mqtt.Pass)
		if err != nil {
			Log("error", fmt.Sprintf("Failed to send to MQTT: %s", err))
		}
	}
}

func Log(level, msg string) {
	switch level {
	case "info":
		fmt.Printf("\x1b[32m%s [INFO] %s\x1b[0m\n", time.Now().Format("15:04:05"), msg)
	case "notice":
		fmt.Printf("\x1b[35m%s [NOTICE] %s\x1b[0m\n", time.Now().Format("15:04:05"), msg)
	case "event":
		fmt.Printf("\x1b[34m%s [EVENT] %s\x1b[0m\n", time.Now().Format("15:04:05"), msg)
	case "error":
		fmt.Printf("\x1b[31m%s [ERROR] %s\x1b[0m\n", time.Now().Format("15:04:05"), msg)
	case "warning":
		fmt.Printf("\x1b[33m%s [WARNING] %s\x1b[0m\n", time.Now().Format("15:04:05"), msg)
	case "debug":
		if globalConfig.PrintDebug {
			fmt.Printf("\x1b[36m%s [DEBUG] %s\x1b[0m\n", time.Now().Format("15:04:05"), msg)
		}
	default:
		fmt.Printf("%s [UNKNOWN] %s\n", time.Now().Format("15:04:05"), msg)
	}
}

func getStreamInfo(rtspURL string) (StreamInfo, error) {
	// Create a context that will time out
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe", "-rtsp_transport", "tcp", "-v", "quiet", "-print_format", "json", "-show_streams", rtspURL)
	output, err := cmd.Output()
	if err != nil {
		Log("debug", fmt.Sprintf("ffprobe output: %s", output))
		return StreamInfo{}, err
	}

	Log("debug", fmt.Sprintf("ffprobe url: %s output: %s", rtspURL, output))

	// Unmarshal into a temporary structure to get the raw frame rate
	var rawInfo struct {
		Streams []struct {
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &rawInfo); err != nil {
		return StreamInfo{}, err
	}

	// Process the streams, converting the frame rate and filtering as needed
	var info StreamInfo
	for _, stream := range rawInfo.Streams {
		if stream.Width == 0 || stream.Height == 0 {
			continue // Skip streams with zero values
		}
		frParts := strings.Split(stream.RFrameRate, "/")
		if len(frParts) == 2 {
			numerator, err1 := strconv.Atoi(frParts[0])
			denominator, err2 := strconv.Atoi(frParts[1])
			if err1 != nil || err2 != nil || denominator == 0 {
				return StreamInfo{}, fmt.Errorf("invalid frame rate: %s", stream.RFrameRate)
			}
			frameRate := float64(numerator) / float64(denominator) // Calculate FPS
			info.Streams = append(info.Streams, struct {
				Width      int     `json:"width"`
				Height     int     `json:"height"`
				CodecType  string  `json:"codec_type"`
				CodecName  string  `json:"codec_name"`
				RFrameRate float64 `json:"-"`
			}{
				Width:      stream.Width,
				Height:     stream.Height,
				CodecType:  stream.CodecType,
				CodecName:  stream.CodecName,
				RFrameRate: frameRate,
			})
		}
	}

	return info, nil
}

func CheckFFmpegAndFFprobe() (bool, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		// Print PATH
		path := os.Getenv("PATH")
		Log("error", fmt.Sprintf("PATH: %s", path))
		return false, fmt.Errorf("ffmpeg binary not found: %w", err)
	}

	if _, err := exec.LookPath("ffprobe"); err != nil {
		// Print PATH
		path := os.Getenv("PATH")
		Log("error", fmt.Sprintf("PATH: %s", path))
		return false, fmt.Errorf("ffprobe binary not found: %w", err)
	}

	return true, nil
}

func processRTSPFeed(rtspURL string, msgChannel chan<- FrameMsg) {
	cmd := exec.Command(
		"ffmpeg",
		"-rtsp_transport", "tcp",
		"-re",
		"-i", rtspURL,
		"-analyzeduration", "1000000",
		"-probesize", "1000000",
		"-vf", `select=not(mod(n\,5))`,
		"-fps_mode", "vfr",
		"-c:v", "png",
		"-f", "image2pipe",
		"-",
	)
	stderrBuffer := &bytes.Buffer{}
	cmd.Stderr = stderrBuffer

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		msgChannel <- FrameMsg{Error: err.Error()}
		return
	}
	defer pipe.Close()

	err = cmd.Start()
	if err != nil {
		msgChannel <- FrameMsg{Error: err.Error()}
		return
	}

	frameCount := 0
	frameData := bytes.NewBuffer(nil)
	isFrameStarted := false

	buffer := make([]byte, 8192) // Buffer size
	for {
		n, err := pipe.Read(buffer)
		if err == io.EOF {
			break
		} else if err != nil {
			msgChannel <- FrameMsg{Error: err.Error()}
			return
		}

		frameData.Write(buffer[:n])

		if bytes.HasPrefix(frameData.Bytes(), []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
			isFrameStarted = true
		}

		if isFrameStarted && bytes.HasSuffix(frameData.Bytes(), []byte{0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}) {
			img, err := png.Decode(bytes.NewReader(frameData.Bytes()))
			if err != nil {
				msgChannel <- FrameMsg{Error: "Failed to decode PNG: " + err.Error()}
			} else {
				msgChannel <- FrameMsg{Frame: img}
			}

			frameCount++
			frameData.Reset()
			isFrameStarted = false
		}

		if frameData.Len() > 2*1024*1024 {
			startIdx := bytes.Index(frameData.Bytes(), []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
			if startIdx != -1 {
				frameData.Next(startIdx)
				isFrameStarted = true
			} else {
				frameData.Reset()
				isFrameStarted = false
			}
		}
	}

	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		}
		msgChannel <- FrameMsg{Error: "FFmpeg exited with error: " + err.Error(), ExitCode: exitCode}
	}

	if stderrBuffer.Len() > 0 {
		msgChannel <- FrameMsg{Error: "FFmpeg STDERR: " + stderrBuffer.String()}
	}
}

// isKeyFrame 检测 MPEG-TS 数据包中是否包含关键帧标记
// MPEG-TS 包大小为 188 字节，同步字节为 0x47
func containsKeyFrame(data []byte) bool {
	// 遍历所有 TS 包
	for i := 0; i+188 <= len(data); i += 188 {
		// 检查同步字节
		if data[i] != 0x47 {
			continue
		}

		// 检查 payload unit start indicator (PUSI)
		if (data[i+1] & 0x40) != 0 {
			// 检查 adaptation field 和 payload 存在
			adaptationControl := (data[i+3] >> 4) & 0x03
			if adaptationControl == 0x02 || adaptationControl == 0x03 {
				// 有 adaptation field
				adaptationLength := int(data[i+4])
				if adaptationLength > 0 && i+5 < len(data) {
					// 检查 random access indicator (关键帧标记)
					if (data[i+5] & 0x40) != 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

func recordRTSPStream(rtspURL string, controlChannel <-chan RecordMsg, prebufferDuration time.Duration) {
	var file *os.File
	recording := false

	cmd := exec.Command("ffmpeg",
    "-rtsp_transport", "tcp",
    "-buffer_size", "1024000",        // 增加缓冲区大小
    "-max_delay", "500000",           // 最大延迟 0.5秒
    "-fflags", "+genpts+discardcorrupt", // 生成 PTS 并丢弃损坏的包
    "-avoid_negative_ts", "make_zero", // 避免负时间戳
    "-use_wallclock_as_timestamps", "1", // 使用墙钟时间戳
    "-i", rtspURL,
    "-c", "copy",
    "-copyts",                        // 复制时间戳
    "-start_at_zero",                 // 从零开始时间戳
    "-f", "mpegts",
    "pipe:1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		Log("error", fmt.Sprintf("Error creating pipe: %v", err))
		return
	}

	err = cmd.Start()
	if err != nil {
		Log("error", fmt.Sprintf("Error starting ffmpeg: %v", err))
		return
	}

	defer func() {
		if recording && file != nil {
			file.Close()
		}
		cmd.Wait()
	}()

	type chunkInfo struct {
		Data       []byte
		Time       time.Time
		HasKeyFrame bool
	}

	bufferSize := 4096
	prebuffer := make([]chunkInfo, 0)
	buffer := make([]byte, bufferSize)
	lastKeyFrameIndex := -1

	for {
		select {
		case msg := <-controlChannel:
			if msg.Record && !recording {
				file, err = os.Create(msg.Filename)
				if err != nil {
					log.Fatal(err)
					return
				}

				// 找到最近的关键帧位置，从关键帧开始写入
				startIndex := 0
				if lastKeyFrameIndex >= 0 && lastKeyFrameIndex < len(prebuffer) {
					startIndex = lastKeyFrameIndex
					Log("info", fmt.Sprintf("Starting recording from keyframe at index %d/%d", startIndex, len(prebuffer)))
				} else {
					Log("warning", "No keyframe found in prebuffer, starting from beginning")
				}

				// 从关键帧位置开始写入预缓冲数据
				for i := startIndex; i < len(prebuffer); i++ {
					_, err := file.Write(prebuffer[i].Data)
					if err != nil {
						log.Fatal(err)
						return
					}
				}
				recording = true
			} else if !msg.Record && recording {
				file.Close()
				recording = false
			}

		default:
			n, err := pipe.Read(buffer)
			if err != nil {
				if err == io.EOF {
					return
				}
				log.Fatal(err)
				return
			}

			// Prebuffer handling
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			timestamp := time.Now()
			hasKeyFrame := containsKeyFrame(chunk)

			prebuffer = append(prebuffer, chunkInfo{
				Data:       chunk,
				Time:       timestamp,
				HasKeyFrame: hasKeyFrame,
			})

			// 更新最后一个关键帧的索引
			if hasKeyFrame {
				lastKeyFrameIndex = len(prebuffer) - 1
			}

			// 移除过期的数据块，但保留至少一个关键帧
			for len(prebuffer) > 1 && timestamp.Sub(prebuffer[0].Time) > prebufferDuration {
				// 如果要删除的是最后一个关键帧之前的数据，需要更新索引
				if lastKeyFrameIndex > 0 {
					lastKeyFrameIndex--
				}
				prebuffer = prebuffer[1:]
			}

			if recording && file != nil {
				_, err := file.Write(buffer[:n])
				if err != nil {
					log.Fatal(err)
					return
				}
			}
		}
	}
}

func recodeToMP4(inputFile string) (string, error) {
	// Check if the input file has a .ts extension
	if !strings.HasSuffix(inputFile, ".ts") {
		return "", fmt.Errorf("input file must have a .ts extension. Got: %s", inputFile)
	}

	// Remove the .ts extension and replace it with .mp4
	outputFile := strings.TrimSuffix(inputFile, ".ts") + ".mp4"

	var cmd *exec.Cmd
	// Create the FFmpeg command
	if globalConfig.Video.OnlyRemuxMp4 {
		if runtimeConfig.CodecName == "hevc" {
			cmd = exec.Command("ffmpeg", "-i", inputFile,
				"-c:v", "copy",
				"-c:a", "aac",
				"-tag:v", "hvc1",
				"-movflags", "+faststart",
				"-hls_segment_type", "fmp4",
				outputFile)
		} else {
			cmd = exec.Command("ffmpeg", "-i", inputFile, "-c", "copy", outputFile)
		}
	} else {
		cmd = exec.Command("ffmpeg", "-i", inputFile, "-c:v", "libx264", "-c:a", "aac", outputFile)
	}

	// Capture the standard output and standard error
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("FFmpeg command failed: %v\n%s", err, output)
	}

	return outputFile, nil
}

func main() {
	ptime := prettyTimer.NewTimingStats()
	// Check if there is a config file argument, if there isnt give error and exit
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Not enough arguments provided\n")
		fmt.Println("Usage: firescrew [configfile]")
		fmt.Println("  -t, --template, t\tPrints the template config to stdout")
		fmt.Println("  -h, --help, h\t\tPrints this help message")
		fmt.Println("  -s, --serve, s\tStarts the web server, requires: [path] [addr]")
		return
	}

	switch os.Args[1] {
	case "-t", "--template", "t":
		// Dump template config to stdout
		printTemplateFile()
		return
	case "-h", "--help", "h":
		// Print help
		fmt.Println("Usage: firescrew [configfile]")
		fmt.Println("  -t, --template, t\tPrints the template config to stdout")
		fmt.Println("  -h, --help, h\t\tPrints this help message")
		fmt.Println("  -s, --serve, s\tStarts the web server, requires: [path] [addr]")
		fmt.Println("  -v, --version, v\tPrints the version")
		fmt.Println("  -update, --update, update\tUpdates firescrew to the latest version")
		return
	case "-s", "--serve", "s":
		// This requires 2 more params, a path to files and an addr in form :8080
		// Check if those params are provided if not give help message
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "Not enough arguments provided\n")
			fmt.Fprintf(os.Stderr, ("Usage: firescrew -s [path] [addr]\n"))
			return
		}
		err := firescrewServe.Serve(os.Args[2], os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error starting server: %v\n", err)
			return
		}
		os.Exit(1)
	case "-v", "--version", "v":
		// Print version
		fmt.Println(Version)
		os.Exit(0)
	case "-update", "--update", "update":
		// Determine OS and ARCH
		osRelease := runtime.GOOS
		arch := runtime.GOARCH
		downloadUrl := fmt.Sprintf("https://gh-proxy.com/https://github.com/kingui1106/firescrew/releases/download/latest/firescrew.%s.%s", osRelease, arch)
		fmt.Println("Downloading from: ", downloadUrl)
		// Build URL
		e := tuna.SelfUpdate(downloadUrl)
		if e != nil {
			fmt.Println(e)
			os.Exit(1)
		}

		fmt.Println("Updated!")
		os.Exit(0)
	}

	// Read the config file
	globalConfig = readConfig(os.Args[1])

	// 初始化数据库连接
	err := initDB()
	if err != nil {
		Log("error", fmt.Sprintf("数据库初始化失败: %s", err))
		os.Exit(2)
	}
	
	// 设置程序退出时关闭数据库连接
	defer closeDB()

	// Check if ffmpeg/ffprobe binaries are available
	_, err = CheckFFmpegAndFFprobe()
	if err != nil {
		Log("error", fmt.Sprintf("Unable to find ffmpeg/ffprobe binaries. Please install them: %s", err))
		os.Exit(2)
	}

	if globalConfig.LoStreamParamBypass.Width == 0 || globalConfig.LoStreamParamBypass.Height == 0 || globalConfig.LoStreamParamBypass.FPS == 0 {
		// Print HI/LO stream details
		hiResStreamInfo, err := getStreamInfo(globalConfig.HiResDeviceUrl)
		if err != nil {
			Log("error", fmt.Sprintf("Error getting stream info: ffprobe: %v", err))
			os.Exit(3)
		}

		if len(hiResStreamInfo.Streams) == 0 {
			Log("error", fmt.Sprintf("No HI res streams found at %s", globalConfig.HiResDeviceUrl))
			os.Exit(3)
		}

		// Find stream with codec_type: video
		streamIndex := -1
		for index, stream := range hiResStreamInfo.Streams {
			if stream.CodecType == "video" {
				streamIndex = index
				runtimeConfig.CodecName = stream.CodecName
				if globalConfig.Video.OnlyRemuxMp4 {
					if stream.CodecName != "h264" {
						Log("warning", fmt.Sprintf("OnlyRemuxMp4 is enabled but the stream codec is not h264 or h265. Your videos may not play in WebUI. Codec: %s", stream.CodecName))
					}
				}
				break
			}
		}

		if streamIndex == -1 {
			Log("error", fmt.Sprintf("No video stream found at %s", globalConfig.HiResDeviceUrl))
			os.Exit(3)
		}

		runtimeConfig.HiResStreamParams = StreamParams{
			Width:  hiResStreamInfo.Streams[streamIndex].Width,
			Height: hiResStreamInfo.Streams[streamIndex].Height,
			FPS:    hiResStreamInfo.Streams[streamIndex].RFrameRate,
		}
	} else {
		runtimeConfig.HiResStreamParams = globalConfig.HiStreamParamBypass
	}

	if globalConfig.LoStreamParamBypass.Width == 0 || globalConfig.LoStreamParamBypass.Height == 0 || globalConfig.LoStreamParamBypass.FPS == 0 {
		loResStreamInfo, err := getStreamInfo(globalConfig.DeviceUrl)
		if err != nil {
			Log("error", fmt.Sprintf("Error getting stream info: %v", err))
			os.Exit(3)
		}

		if len(loResStreamInfo.Streams) == 0 {
			Log("error", fmt.Sprintf("No LO res streams found at %s", globalConfig.DeviceUrl))
			os.Exit(3)
		}

		// Find stream with codec_type: video
		streamIndex := -1
		for index, stream := range loResStreamInfo.Streams {
			if stream.CodecType == "video" {
				streamIndex = index
				break
			}
		}

		if streamIndex == -1 {
			Log("error", fmt.Sprintf("No video stream found at %s", globalConfig.DeviceUrl))
			os.Exit(3)
		}

		runtimeConfig.LoResStreamParams = StreamParams{
			Width:  loResStreamInfo.Streams[streamIndex].Width,
			Height: loResStreamInfo.Streams[streamIndex].Height,
			FPS:    loResStreamInfo.Streams[streamIndex].RFrameRate,
		}
	} else {
		runtimeConfig.LoResStreamParams = globalConfig.LoStreamParamBypass
	}

	// Print stream info from runtimeConfig
	Log("info", "******************** STREAM INFO ********************")
	Log("info", fmt.Sprintf("Lo-Res Stream Resolution: %dx%d FPS: %.2f", runtimeConfig.LoResStreamParams.Width, runtimeConfig.LoResStreamParams.Height, runtimeConfig.LoResStreamParams.FPS))
	Log("info", fmt.Sprintf("Hi-Res Stream Resolution: %dx%d FPS: %.2f", runtimeConfig.HiResStreamParams.Width, runtimeConfig.HiResStreamParams.Height, runtimeConfig.HiResStreamParams.FPS))
	Log("info", "*****************************************************")

	// Define motion mutex
	runtimeConfig.MotionMutex = &sync.Mutex{}

	// Copy assets to local filesystem
	path := copyAssetsToTemp()
	// Start the object detector

	if globalConfig.Motion.OnnxModel != "" {
		var err error
		runtimeConfig.ObjectPredictClient, err = ob.Init(ob.Config{Model: "yolov8n", EnableCoreMl: globalConfig.Motion.OnnxEnableCoreMl})
		if err != nil {
			fmt.Println("Cannot init model:", err)
			return
		}

		defer runtimeConfig.ObjectPredictClient.Close() // Cleanup files

	} else {
		if globalConfig.Motion.NetworkObjectDetectServer == "" {
			globalConfig.Motion.NetworkObjectDetectServer = "127.0.0.1:8555"
			go startObjectDetector(path + "/" + globalConfig.Motion.EmbeddedObjectScript)
			// Set networkObjectDetectServer path to 127.0.0.1:8555
			// time.Sleep(10 * time.Second) // Give time to kill old instance if still running
			// Wait until tcp connection is works to globalConfig.Motion.NetworkObjectDetectServer
			Log("info", "Waiting for object detector to come up")
			if !runtimeConfig.modelReady {
				for {
					conn, err := net.DialTimeout("tcp", globalConfig.Motion.NetworkObjectDetectServer, 1*time.Second)
					if err != nil {
						Log("warning", fmt.Sprintf("Waiting for object detector to start: %v", err))
						time.Sleep(1 * time.Second)
					} else {
						conn.Close()
						break
					}
				}
			}
		} else {
			Log("info", fmt.Sprintf("Checking connection to: %s", globalConfig.Motion.NetworkObjectDetectServer))
			for {
				conn, err := net.DialTimeout("tcp", globalConfig.Motion.NetworkObjectDetectServer, 1*time.Second)
				if err != nil {
					Log("warning", fmt.Sprintf("Waiting for %s to respond: %v", globalConfig.Motion.NetworkObjectDetectServer, err))
					time.Sleep(1 * time.Second)
				} else {
					conn.Close()
					break
				}
			}
		}
	}

	stream = mjpeg.NewStream()
	if globalConfig.EnableOutputStream {
		go startWebcamStream(stream)
	}

	// Define the last image
	imgLast := image.NewRGBA(image.Rect(0, 0, runtimeConfig.HiResStreamParams.Width, runtimeConfig.HiResStreamParams.Height))

	// Start HI Res prebuffering
	runtimeConfig.HiResControlChannel = make(chan RecordMsg)
	go func() {
		for {
			recordRTSPStream(globalConfig.HiResDeviceUrl, runtimeConfig.HiResControlChannel, time.Duration(globalConfig.Motion.PrebufferSeconds)*time.Second)
			// defer close(runtimeConfig.HiResControlChannel)
			time.Sleep(5 * time.Second)
			Log("warning", "Restarting HI RTSP feed")
		}
	}()

	frameChannel := make(chan FrameMsg)
	go func(frameChannel chan FrameMsg) {
		for {
			processRTSPFeed(globalConfig.DeviceUrl, frameChannel)
			// Log("warning", "EXITED")
			//*********** EXITS BELOW ***********//
			time.Sleep(5 * time.Second)
			Log("warning", "Restarting LO RTSP feed")
		}
	}(frameChannel)
	// go dumpRtspFrames(globalConfig.DeviceUrl, "/Volumes/RAMDisk/", 4) // 1 means mod every nTh frame
	// go readFramesFromRam(frameChannel, "/Volumes/RAMDisk/")

	for msg := range frameChannel {
		if msg.Error != "" {
			Log("error", msg.Error)
			continue
		}

		if msg.Frame != nil {
			ptime.Start() // DEBUG TIMER

			rgba, ok := msg.Frame.(*image.RGBA)
			if !ok {
				// Convert to RGBA if it's not already
				rgba = image.NewRGBA(msg.Frame.Bounds())
				draw.Draw(rgba, rgba.Bounds(), msg.Frame, msg.Frame.Bounds().Min, draw.Src)
			}

			// Handle all motion stuff here
			if runtimeConfig.MotionTriggered || (!runtimeConfig.MotionTriggered && CountChangedPixels(rgba, imgLast, uint8(30)) > int(globalConfig.PixelMotionAreaThreshold)) { // Use short-circuit to bypass pixel count if event is already triggered, otherwise we may not be able to identify all objects if motion is triggered
				// If its been more than globalConfig.Motion.EventGap seconds since the last motion event, untrigger
				if runtimeConfig.MotionTriggered && time.Since(runtimeConfig.MotionTriggeredLast) > time.Duration(globalConfig.Motion.EventGap)*time.Second {
					go endMotionEvent() // End the motion event
				}

				// Only run this on every Nth frame
				if predictFrameCounter%everyNthFrame == 0 {
					if predictFrameCounter > 10000 {
						predictFrameCounter = 0
					}
					if msg.Frame != nil {

						var predict []Prediction
						var err error
						// If globalConfig.Motion.OnnxModel is blank run this
						// Send data to objectPredict
						if globalConfig.Motion.OnnxModel == "" {
							predict, err = objectPredict(msg.Frame)
							if err != nil {
								Log("error", fmt.Sprintf("Error running objectPredict: %v", err))
								return
							}
							performDetectionOnObject(nil, rgba, predict)
						} else {
							timer := time.Now()
							objects, resizedImage, err := runtimeConfig.ObjectPredictClient.Predict(msg.Frame)
							if err != nil {
								fmt.Println("Cannot predict:", err)
								return
							}

							// Detect took
							took := time.Since(timer).Milliseconds()

							for _, object := range objects {
								pred := Prediction{
									Object:     object.ClassID,
									ClassName:  object.ClassName,
									Box:        []float32{object.X1, object.Y1, object.X2, object.Y2},
									Top:        int(object.Y1),
									Bottom:     int(object.Y2),
									Left:       int(object.X1),
									Right:      int(object.X2),
									Confidence: object.Confidence,
									Took:       float64(took),
								}
								predict = append(predict, pred)
							}
							performDetectionOnObject(rgba, resizedImage, predict)
						}
						calcInferenceStats(predict) // Calculate inference stats

						// FIX THIS Its taking way too long to process
						// if len(predict) > 0 {
						// 	// Notify in realtime about detected objects
						// 	type Event struct {
						// 		Type             string       `json:"type"`
						// 		Timestamp        time.Time    `json:"timestamp"`
						// 		PredictedObjects []Prediction `json:"predicted_objects"`
						// 	}

						// 	eventRaw := Event{
						// 		Type:             "objects_predicted",
						// 		Timestamp:        time.Now(),
						// 		PredictedObjects: predict,
						// 	}
						// 	eventJson, err := json.Marshal(eventRaw)
						// 	if err != nil {
						// 		Log("error", fmt.Sprintf("Error marshalling object_predicted event: %v", err))
						// 		return
						// 	}
						// 	go eventHandler("objects_detected", eventJson)
						// }

						// if len(predict) > 0 {
						// 	fname := fmt.Sprintf("%d.jpg", predictFrameCounter)
						// 	saveJPEG(filepath.Join(globalConfig.Video.HiResPath, fname), rgba, 100)
						// }

					}

					predictFrameCounter++
				}
			}

			if globalConfig.EnableOutputStream {
				streamImage(rgba, stream) // Stream the image to the web
			}

			imgLast = rgba // Set the last image to the current image

			ptime.Finish() // DEBUG TIMER
			// ptime.PrintStats() // DEBUG TIMER

		}
	}

}

func performDetectionOnObject(originalFrame *image.RGBA, frame *image.RGBA, prediction []Prediction) {
	now := time.Now()
	for _, predict := range prediction {
		// If class is not within LookForClasses, skip it
		if len(globalConfig.Motion.LookForClasses) > 0 {
			found := false
			for _, filterClass := range globalConfig.Motion.LookForClasses {
				if predict.ClassName == filterClass {
					found = true
				}
			}
			if !found {
				continue
			}
		}

		if predict.Confidence < float32(globalConfig.Motion.ConfidenceMinThreshold) {
			continue
		}

		rect := image.Rect(predict.Left, predict.Top, predict.Right, predict.Bottom)
		center := image.Pt((predict.Left+predict.Right)/2, (predict.Top+predict.Bottom)/2)

		object := TrackedObject{
			BBox:          rect,
			Center:        center,
			LastMoved:     now,
			Area:          float64(rect.Dx() * rect.Dy()),
			Class:         predict.ClassName,
			Confidence:    predict.Confidence,
			FirstSeen:     now,
			InitialCenter: center,
			MovementVector: image.Pt(0, 0),
			Direction:     "unknown",
		}

		exists := findObjectPosition(object)
		if !exists {

			// Check if this object is within the areas of interest
			for _, ignoreAreaClass := range globalConfig.IgnoreAreasClasses {
				for _, class := range ignoreAreaClass.Class {
					if class == object.Class {
						if object.Center.X > ignoreAreaClass.Left && object.Center.X < ignoreAreaClass.Right && object.Center.Y > ignoreAreaClass.Top && object.Center.Y < ignoreAreaClass.Bottom {
							// This object is within an ignore area, skip it
							// fmt.Printf("Ignoring object %s @ %d|%f\n", object, object.Center)
							// Log("warning", fmt.Sprintf("IGNORING OBJECT @ %d|%f [%s|%f]", object.Center, object.Area, object.Class, object.Confidence))
							return
						}
					}
				}
			}

			Log("info", fmt.Sprintf("TRIGGERED NEW OBJECT @ COORD: %d AREA: %f [%s|%f] DIRECTION: %s", object.Center, object.Area, object.Class, object.Confidence, object.Direction))
			if !runtimeConfig.MotionTriggered {
				// Lock mutex
				runtimeConfig.MotionMutex.Lock()
				runtimeConfig.MotionTriggered = true
				runtimeConfig.MotionTriggeredLast = now
				runtimeConfig.MotionVideo.CameraName = globalConfig.CameraName
				runtimeConfig.MotionVideo.MotionStart = now
				// Generate random string filename for runtimeConfig.MotionVideo.Filename
				runtimeConfig.MotionVideo.ID = generateRandomString(15)
				runtimeConfig.MotionVideo.Objects = append(runtimeConfig.MotionVideo.Objects, object)
				runtimeConfig.MotionVideo.VideoFile = fmt.Sprintf("clip_%s.ts", runtimeConfig.MotionVideo.ID)                                                            // Set filename for video file
				runtimeConfig.HiResControlChannel <- RecordMsg{Record: true, Filename: filepath.Join(globalConfig.Video.HiResPath, runtimeConfig.MotionVideo.VideoFile)} // Start recording

				// Notify in realtime about detected objects
				eventRaw := Event{
					Type:                "motion_started",
					Timestamp:           time.Now(),
					MotionTriggeredLast: time.Now(),
					ID:                  runtimeConfig.MotionVideo.ID,
					MotionStart:         runtimeConfig.MotionVideo.MotionStart,
					Objects:             runtimeConfig.MotionVideo.Objects,
					CameraName:          runtimeConfig.MotionVideo.CameraName,
				}
				eventJson, err := json.Marshal(eventRaw)
				if err != nil {
					Log("error", fmt.Sprintf("Error marshalling motion_started event: %v", err))
					return
				}
				eventHandler("motion_start", eventJson)

				// Send pushover notification
				if globalConfig.Notifications.EnablePushoverAlerts {
					frameCopy := *frame

					ob.DrawRectangle(&frameCopy, rect, color.RGBA{255, 165, 0, 255}, 2) // Draw orange rectangle

					pt := image.Pt(predict.Left, predict.Top-5)
					if predict.Top-5 < 0 {
						pt = image.Pt(predict.Left, predict.Top+20) // if the box is too close to the top of the image, put the label inside the box
					}
					ob.AddLabelWithTTF(&frameCopy, fmt.Sprintf("%s %.2f", predict.ClassName, predict.Confidence), pt, color.RGBA{255, 165, 0, 255}, 12.0) // Orange size 12 font

					// Send pushover notification
					err := sendPushoverNotification(globalConfig.Notifications.PushoverUserKey, globalConfig.Notifications.PushoverAppToken, "Motion detected!", &frameCopy)
					if err != nil {
						Log("error", fmt.Sprintf("Error sending pushover notification: %v", err))
					}

				}

				// Unlock mutex
				runtimeConfig.MotionMutex.Unlock()
			} else {
				// Lock mutex
				runtimeConfig.MotionMutex.Lock()
				runtimeConfig.MotionTriggeredLast = now
				runtimeConfig.MotionVideo.Objects = append(runtimeConfig.MotionVideo.Objects, object)

				// Notify in realtime about detected objects
				eventRaw := Event{
					Type:                "motion_update",
					Timestamp:           time.Now(),
					MotionTriggeredLast: time.Now(),
					ID:                  runtimeConfig.MotionVideo.ID,
					MotionStart:         runtimeConfig.MotionVideo.MotionStart,
					Objects:             runtimeConfig.MotionVideo.Objects,
					CameraName:          runtimeConfig.MotionVideo.CameraName,
				}
				eventJson, err := json.Marshal(eventRaw)
				if err != nil {
					Log("error", fmt.Sprintf("Error marshalling motion_update event: %v", err))
					return
				}
				eventHandler("motion_update", eventJson)

				// Unlock mutex
				runtimeConfig.MotionMutex.Unlock()
			}

			// Log("error", fmt.Sprintf("STORED %d OBJECTS", len(runtimeConfig.MotionVideo.Objects)))

			ob.DrawRectangle(frame, rect, color.RGBA{255, 165, 0, 255}, 2) // Draw orange rectangle

			pt := image.Pt(predict.Left, predict.Top-5)
			if predict.Top-5 < 0 {
				pt = image.Pt(predict.Left, predict.Top+20) // if the box is too close to the top of the image, put the label inside the box
			}
			ob.AddLabelWithTTF(frame, fmt.Sprintf("%s %.2f", predict.ClassName, predict.Confidence), pt, color.RGBA{255, 165, 0, 255}, 12.0) // Orange size 12 font

			// Store snapshot of the object
			if runtimeConfig.MotionVideo.ID != "" {
				snapshotFilename := fmt.Sprintf("snap_%s_%s.jpg", runtimeConfig.MotionVideo.ID, generateRandomString(4))
				runtimeConfig.MotionVideo.Snapshots = append(runtimeConfig.MotionVideo.Snapshots, snapshotFilename)

				if originalFrame != nil {
					origWidth, origHeight := ob.GetImageDimensions(originalFrame)
					// Resize the image to the original dimensions
					frame = ob.RemovePadding(frame, origWidth, origHeight)
				}

				// Add frames for gif
				copyFrame := *frame
				gifSliceMutex.Lock()
				gifSlice = append(gifSlice, copyFrame)
				gifSliceMutex.Unlock()

				saveJPEG(filepath.Join(globalConfig.Video.HiResPath, snapshotFilename), frame, 100)
			} else {
				Log("warning", "runtimeConfig.MotionVideo.ID is empty, not writing snapshot. This shouldnt happen.")
			}
		}
	}
}

// calculateIoU calculates the Intersection over Union (IoU) of two bounding boxes
// Returns a value between 0.0 (no overlap) and 1.0 (identical boxes)
func calculateIoU(box1, box2 image.Rectangle) float64 {
	// Calculate intersection
	intersectMinX := max(box1.Min.X, box2.Min.X)
	intersectMinY := max(box1.Min.Y, box2.Min.Y)
	intersectMaxX := min(box1.Max.X, box2.Max.X)
	intersectMaxY := min(box1.Max.Y, box2.Max.Y)

	// Check if there is an intersection
	if intersectMinX >= intersectMaxX || intersectMinY >= intersectMaxY {
		return 0.0 // No intersection
	}

	// Calculate intersection area
	intersectionArea := float64((intersectMaxX - intersectMinX) * (intersectMaxY - intersectMinY))

	// Calculate areas of both boxes
	area1 := float64(box1.Dx() * box1.Dy())
	area2 := float64(box2.Dx() * box2.Dy())

	// Handle edge case of zero area boxes
	if area1 <= 0 || area2 <= 0 {
		return 0.0
	}

	// Calculate union area
	unionArea := area1 + area2 - intersectionArea

	// Return IoU
	if unionArea <= 0 {
		return 0.0
	}

	return intersectionArea / unionArea
}

// Function that goes over lastPositions and checks if any of them are within of a threshold of the current center
// Uses IoU (Intersection over Union) as primary matching method, with center-distance as fallback
func findObjectPosition(object TrackedObject) bool {
	const iouThreshold = 0.3 // IoU threshold for matching

	// Check if this object has been seen before
	for i := 0; i < len(lastPositions); i++ {
		// Skip if class doesn't match
		if object.Class != lastPositions[i].Class {
			continue
		}

		// Primary matching: IoU-based matching
		iou := calculateIoU(object.BBox, lastPositions[i].BBox)
		
		// Calculate center distance for fallback matching
		distance := math.Sqrt(float64((object.Center.X-lastPositions[i].Center.X)*(object.Center.X-lastPositions[i].Center.X) + (object.Center.Y-lastPositions[i].Center.Y)*(object.Center.Y-lastPositions[i].Center.Y)))

		// Match if IoU > threshold OR (center distance < threshold AND area difference is reasonable)
		matched := false
		if iou > iouThreshold {
			// IoU-based match
			matched = true
			// Only log if object actually moved (distance > 5 pixels) to reduce noise
			if distance > 5 {
				Log("debug", fmt.Sprintf("IoU MATCH: %.3f for %s @ %v", iou, object.Class, object.Center))
			}
		} else if distance < globalConfig.ObjectCenterMovementThreshold {
			// Fallback: center-distance based matching with relaxed area threshold
			// Allow matching even if size changes significantly (for objects moving toward/away from camera)
			areaDiff := math.Abs(object.Area - lastPositions[i].Area)
			maxArea := math.Max(object.Area, lastPositions[i].Area)
			areaRatio := areaDiff / maxArea
			
			// Match if area change is less than 50% OR area diff is within absolute threshold
			if areaRatio < 0.5 || areaDiff < globalConfig.ObjectAreaThreshold {
				matched = true
				Log("debug", fmt.Sprintf("CENTER MATCH: dist=%.1f, areaDiff=%.1f (%.1f%%) for %s", distance, areaDiff, areaRatio*100, object.Class))
			}
		}

		if matched {
			// Check if object has been tracked for too long (max 30 seconds)
			// This handles stationary objects that should be finalized
			trackingDuration := time.Since(lastPositions[i].FirstSeen)
			if trackingDuration > 30*time.Second {
				// Object has been tracked for too long, treat as new detection
				// This will trigger a new record for stationary objects
				Log("info", fmt.Sprintf("STATIONARY TIMEOUT [%s] @ %v tracked for %.1fs, resetting", lastPositions[i].ID, lastPositions[i].Center, trackingDuration.Seconds()))
				// Remove the old tracking entry
				lastPositions = append(lastPositions[:i], lastPositions[i+1:]...)
				// Don't return true - let it fall through to create new object
				break
			}

			// This means a match, update the object with direction tracking
			// Preserve the ID, initial position and first seen time
			object.ID = lastPositions[i].ID
			object.FirstSeen = lastPositions[i].FirstSeen
			object.InitialCenter = lastPositions[i].InitialCenter
			object.FrameCount = lastPositions[i].FrameCount + 1

			// Track maximum confidence
			if object.Confidence > lastPositions[i].MaxConfidence {
				object.MaxConfidence = object.Confidence
			} else {
				object.MaxConfidence = lastPositions[i].MaxConfidence
			}

			// Calculate movement vector from initial position
			object.MovementVector = image.Pt(
				object.Center.X - object.InitialCenter.X,
				object.Center.Y - object.InitialCenter.Y,
			)

			// Determine direction if enabled
			if globalConfig.DirectionDetection.Enabled {
				object.Direction = determineDirection(object)
			}

			// Only log every 100 frames to reduce noise for stationary objects
			if object.FrameCount%100 == 0 || object.FrameCount < 10 {
				Log("debug", fmt.Sprintf("UPDATING OBJECT [%s] @ %v TO %v DIRECTION: %s FRAMES: %d", object.ID, lastPositions[i].Center, object.Center, object.Direction, object.FrameCount))
			}
			lastPositions[i] = object
			return true
		}
	}

	// Clean up lastPositions - remove objects not updated for more than 5 seconds
	for i := len(lastPositions) - 1; i >= 0; i-- {
		if time.Since(lastPositions[i].LastMoved) > 5*time.Second {
			Log("info", fmt.Sprintf("EXPIRING OBJECT [%s] @ %v [%s|%.2f] DIRECTION: %s FRAMES: %d", lastPositions[i].ID, lastPositions[i].Center, lastPositions[i].Class, lastPositions[i].MaxConfidence, lastPositions[i].Direction, lastPositions[i].FrameCount))
			lastPositions = append(lastPositions[:i], lastPositions[i+1:]...)
		}
	}

	// This is a new object, initialize tracking fields
	object.ID = generateRandomString(8) // Generate unique ID for new object
	object.FirstSeen = time.Now()
	object.InitialCenter = object.Center
	object.MovementVector = image.Pt(0, 0)
	object.Direction = "unknown"
	object.MaxConfidence = object.Confidence
	object.FrameCount = 1

	lastPositions = append(lastPositions, object)
	return false
}

// Determine the direction of object movement
// Uses cumulative movement vector from first detection to current position
func determineDirection(object TrackedObject) string {
	// Calculate total movement distance from initial position
	totalMovement := math.Sqrt(float64(object.MovementVector.X*object.MovementVector.X + object.MovementVector.Y*object.MovementVector.Y))

	// Use configured threshold for direction detection
	minMovement := globalConfig.DirectionDetection.MinMovementPixels
	if minMovement <= 0 {
		minMovement = 30.0 // Default to 30 pixels if not configured
	}

	// Log movement info for debugging (only every 50 frames to reduce noise)
	if object.FrameCount%50 == 0 {
		Log("debug", fmt.Sprintf("DIRECTION CHECK [%s]: movement=(%.1f,%.1f) total=%.1f threshold=%.1f",
			object.ID, float64(object.MovementVector.X), float64(object.MovementVector.Y), totalMovement, minMovement))
	}

	// If movement is too small, keep previous direction or return unknown
	if totalMovement < minMovement {
		// If we already have a direction, keep it
		if object.Direction != "" && object.Direction != "unknown" {
			return object.Direction
		}
		return "unknown"
	}

	// Direction detection based on dominant axis movement
	// Positive X = moving right, Negative X = moving left
	// Positive Y = moving down, Negative Y = moving up

	absX := math.Abs(float64(object.MovementVector.X))
	absY := math.Abs(float64(object.MovementVector.Y))

	var rawDirection string

	// Determine primary direction based on larger movement
	if absX > absY {
		// Horizontal movement is dominant
		if object.MovementVector.X > 0 {
			rawDirection = "right"
		} else {
			rawDirection = "left"
		}
	} else {
		// Vertical movement is dominant
		if object.MovementVector.Y > 0 {
			rawDirection = "down"
		} else {
			rawDirection = "up"
		}
	}

	// Map to entering/exiting based on configuration
	// Default mapping: right/down = entering, left/up = exiting
	// This can be customized based on camera setup
	var result string
	switch rawDirection {
	case "right", "down":
		result = "entering"
	case "left", "up":
		result = "exiting"
	default:
		result = "unknown"
	}

	Log("debug", fmt.Sprintf("DIRECTION: movement=(%.1f,%.1f) total=%.1f raw=%s result=%s",
		float64(object.MovementVector.X), float64(object.MovementVector.Y), totalMovement, rawDirection, result))

	return result
}

func streamImage(img *image.RGBA, stream *mjpeg.Stream) {
	// Draw ignore areas from IgnoreAreasClasses
	if globalConfig.StreamDrawIgnoredAreas {
		for _, ignoreAreaClass := range globalConfig.IgnoreAreasClasses {
			// Draw the ignore area
			rect := image.Rect(ignoreAreaClass.Left, ignoreAreaClass.Top, ignoreAreaClass.Right, ignoreAreaClass.Bottom)
			ob.DrawRectangle(img, rect, color.RGBA{255, 0, 0, 0}, 2)
		}
	}

	// Encode the RGBA image to JPEG
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		// Handle encoding error
		return
	}

	// Stream video over HTTP
	stream.UpdateJPEG(buf.Bytes())
}

func startWebcamStream(stream *mjpeg.Stream) {
	// start http server
	http.Handle("/", stream)

	server := &http.Server{
		Addr:         globalConfig.OutputStreamAddr,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Fatal(server.ListenAndServe())
}

func establishConnection() error {
	d := net.Dialer{}
	var err error
	runtimeConfig.objectPredictConn, err = d.Dial("tcp", globalConfig.Motion.NetworkObjectDetectServer)
	return err
}

func objectPredict(imgRaw image.Image) ([]Prediction, error) {
	// Create a channel to communicate the result of the function
	resultChan := make(chan []Prediction, 1)
	errorChan := make(chan error, 1)

	// Start the actual work in a goroutine
	go func() {
		// Start timer
		start := time.Now()

		// Convert the image to a byte array
		buf := new(bytes.Buffer)
		if err := jpeg.Encode(buf, imgRaw, nil); err != nil {
			errorChan <- err
			return
		}
		imgData := buf.Bytes()

		// Check if connection is nil and re-establish it if needed
		if runtimeConfig.objectPredictConn == nil {
			// Re-establish connection
			if err := establishConnection(); err != nil {
				errorChan <- err
				return
			}
		}

		// Check if connection is broken and re-establish it if needed
		if err := runtimeConfig.objectPredictConn.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
			// Re-establish connection
			if err := establishConnection(); err != nil {
				errorChan <- err
				return
			}
		}

		// Send the size of the image data
		size := uint32(len(imgData))
		sizeBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(sizeBytes, size)
		if _, err := runtimeConfig.objectPredictConn.Write(sizeBytes); err != nil {
			// Re-establish connection
			if err := establishConnection(); err != nil {
				errorChan <- err
				return
			}
		}

		// Send the image data
		if _, err := runtimeConfig.objectPredictConn.Write(imgData); err != nil {
			// Re-establish connection
			if err := establishConnection(); err != nil {
				errorChan <- err
				return
			}
		}

		// Read the response
		reader := bufio.NewReader(runtimeConfig.objectPredictConn)
		respData, err := reader.ReadBytes('\n')
		if err != nil {
			errorChan <- err
			return
		}

		// fmt.Printf("RAW: %s\n", respData)

		// Parse the response data as a Prediction
		var preds []Prediction
		if err := json.Unmarshal(respData, &preds); err != nil {
			errorChan <- err
			return
		}

		for i, pred := range preds {
			box := pred.Box
			preds[i].Top = int(box[1])
			preds[i].Bottom = int(box[3])
			preds[i].Left = int(box[0])
			preds[i].Right = int(box[2])
			preds[i].Took = float64(time.Since(start).Milliseconds())
		}

		resultChan <- preds
	}()

	// Wait for the result or the context timeout
	select {
	case result := <-resultChan:
		return result, nil
	case err := <-errorChan:
		Log("debug", fmt.Sprintf("Error running objectPredict: %v", err))
		return nil, err
	}
}

func startObjectDetector(scriptPath string) {
	basePath := filepath.Dir(scriptPath)
	restartCount := 0
	pidFileName := filepath.Base(scriptPath) + ".pid"
	pidFilePath := filepath.Join("/tmp", pidFileName)

	// Read the first line of the script to get the shebang
	file, err := os.Open(scriptPath)
	if err != nil {
		Log("error", fmt.Sprintf("Error opening script: %v", err))
		return
	}
	reader := bufio.NewReader(file)
	shebang, err := reader.ReadString('\n')
	file.Close()
	if err != nil {
		Log("error", fmt.Sprintf("Error reading shebang: %v", err))
		return
	}

	// Extract the interpreter from the shebang
	shebang = strings.TrimSpace(strings.TrimPrefix(shebang, "#!"))
	interpreterArgs := strings.Split(shebang, " ")
	if len(interpreterArgs) < 2 {
		interpreterArgs = []string{"python3"} // Default interpreter if shebang is not found or incorrect
	}

	// Try to read existing PID file
	pidData, err := os.ReadFile(pidFilePath)
	if err == nil {
		pid, err := strconv.Atoi(string(pidData))
		if err == nil {
			process, err := os.FindProcess(pid)
			if err == nil {
				process.Kill() // Try to kill the existing process
			}
		}
	}

	for {
		if restartCount > 3 {
			Log("error", "Embedded python script failed 3 times, giving up")
			os.Exit(1)
		}

		cmd := exec.Command(interpreterArgs[0], append(interpreterArgs[1:], "-u", scriptPath)...)
		cmd.Dir = basePath

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			Log("error", fmt.Sprintf("Error creating StdoutPipe for Cmd: %v", err))
			continue
		}

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		Log("info", "Starting embedded python object server")
		err = cmd.Start()

		if err != nil {
			Log("error", fmt.Sprintf("Error starting Cmd: %v", err))
			continue
		}

		// Write PID to file
		err = os.WriteFile(pidFilePath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
		if err != nil {
			Log("error", fmt.Sprintf("Error writing PID to file: %v", err))
			cmd.Process.Kill()
			continue
		}

		go readOutput(stdout)

		// Create a channel to catch signals to the process
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-c
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			os.Remove(pidFilePath) // Remove PID file
			os.Exit(1)
		}()

		runtimeConfig.modelReady = true // Allow objectPredict to start sending images

		err = cmd.Wait()
		if err != nil {
			Log("error", fmt.Sprintf("Embedded python script failed: %s", stderr.String()))
		} else {
			Log("info", "Embedded python script exited, restarting...")
		}

		os.Remove(pidFilePath) // Remove PID file

		time.Sleep(2 * time.Second)
		restartCount++
	}
}

// Helped function for startObjectDetector
func readOutput(r io.ReadCloser) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// Strip newlines
		out := strings.TrimSuffix(scanner.Text(), "\n")

		// Strip empty lines
		if out == "" {
			continue
		}

		Log("debug", fmt.Sprintf("PYTHON_MODEL_STDOUT: %s", out))
	}
}

// Function that copies assetsFs to /tmp in a random folder and returns path
func copyAssetsToTemp() string {
	// Create a random folder in /tmp
	r := mathRand.New(mathRand.NewSource(time.Now().UnixNano()))
	tempDir := fmt.Sprintf("/tmp/%d", r.Intn(1000)+1)
	os.Mkdir(tempDir, 0755)

	// Delete the temporary directory when the application exits
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		os.RemoveAll(tempDir)
		os.Exit(0)
	}()

	// Copy assets to temp dir
	assets, err := assetsFs.ReadDir("assets") // Read the "assets" directory instead of "."
	if err != nil {
		panic(err)
	}

	for _, asset := range assets {

		if asset.IsDir() {
			continue
		}
		data, err := assetsFs.ReadFile("assets/" + asset.Name()) // Add "assets/" prefix when reading the file
		if err != nil {
			panic(err)
		}

		err = os.WriteFile(filepath.Join(tempDir, asset.Name()), data, 0644)
		if err != nil {
			panic(err)
		}

		// Make *.py files executable
		if filepath.Ext(asset.Name()) == ".py" {
			err = os.Chmod(filepath.Join(tempDir, asset.Name()), 0755)
			if err != nil {
				panic(err)
			}
		}
	}

	return tempDir
}

// This function prints out embedded template.json file
func printTemplateFile() {
	fileBytes, err := assetsFs.ReadFile("assets/template.json")
	if err != nil {
		log.Fatalf("Failed to read template file: %v", err)
	}

	fmt.Println(string(fileBytes))
}

func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	seededRand := mathRand.New(mathRand.NewSource(time.Now().UnixNano()))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

func CountChangedPixels(img1, img2 *image.RGBA, threshold uint8) int {
	if img1.Bounds() != img2.Bounds() {
		return -1
	}

	count := 0
	for y := 0; y < img1.Bounds().Dy(); y++ {
		for x := 0; x < img1.Bounds().Dx(); x++ {
			offset := y*img1.Stride + x*4
			r1, g1, b1 := int(img1.Pix[offset]), int(img1.Pix[offset+1]), int(img1.Pix[offset+2])
			r2, g2, b2 := int(img2.Pix[offset]), int(img2.Pix[offset+1]), int(img2.Pix[offset+2])
			gray1 := (299*r1 + 587*g1 + 114*b1) / 1000
			gray2 := (299*r2 + 587*g2 + 114*b2) / 1000
			diff := gray1 - gray2
			if diff < 0 {
				diff = -diff
			}
			if uint8(diff) > threshold {
				count++
			}
		}
	}

	return count
}

func saveJPEG(filename string, img *image.RGBA, quality int) {
	file, err := os.Create(filename)
	if err != nil {
		Log("error", fmt.Sprintf("File create error: %s", err))
		return
	}
	defer file.Close()

	options := &jpeg.Options{Quality: quality} // Quality ranges from 1 to 100
	err = jpeg.Encode(file, img, options)
	if err != nil {
		Log("error", fmt.Sprintf("JPEG encode error: %s", err))
		return
	}
}

func sendToMQTT(topic string, message string, host string, port int, user string, pass string) error {
	// MQTT client options
	opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("tcp://%s:%d", host, port))

	if user != "" && pass != "" {
		opts.SetUsername(user)
		opts.SetPassword(pass)
	}

	// Create and connect the client
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		Log("error", fmt.Sprintf("Failed to connect to MQTT: %s", token.Error()))
		return token.Error() // Return the connection error
	}
	defer client.Disconnect(250)

	// Publish the message
	token := client.Publish(topic, 0, false, message)
	token.Wait()
	if token.Error() != nil {
		Log("error", fmt.Sprintf("Failed to publish to MQTT: %s", token.Error()))
		return token.Error() // Return the publishing error
	}

	return nil // Return nil if there were no errors
}

func calcInferenceStats(predict []Prediction) {
	// Calculate avg of all predict times for this run
	stats := InferenceStats{}
	stats.Min = math.MaxFloat64
	count := 0

	if len(predict) == 0 {
		return
	}

	for _, prediction := range predict {
		if prediction.Took > float64(1000/everyNthFrame) {
			Log("warning", fmt.Sprintf("Inference took %fms, max ceiling should be: %dms", prediction.Took, 1000/everyNthFrame))
		}

		if prediction.Took > stats.Max {
			stats.Max = prediction.Took
		}

		if prediction.Took < stats.Min {
			stats.Min = prediction.Took
		}

		stats.Avg += prediction.Took
		count++
	}

	stats.Avg = stats.Avg / float64(count)

	runtimeConfig.InferenceTimingBuffer = append(runtimeConfig.InferenceTimingBuffer, stats)

	if len(runtimeConfig.InferenceTimingBuffer) >= interenceAvgInterval {
		statsFinal := InferenceStats{}
		statsFinal.Min = math.MaxFloat64
		statsFinal.Max = 0 // Initialize Max to 0
		// Calculate avg inference time
		for _, inferenceTime := range runtimeConfig.InferenceTimingBuffer {
			statsFinal.Avg += inferenceTime.Avg
			if inferenceTime.Min < statsFinal.Min {
				statsFinal.Min = inferenceTime.Min
			}

			if inferenceTime.Max > statsFinal.Max {
				statsFinal.Max = inferenceTime.Max
			}
		}

		statsFinal.Avg = statsFinal.Avg / float64(len(runtimeConfig.InferenceTimingBuffer))

		// Log avg inference time
		type Event struct {
			Type         string    `json:"type"`
			Timestamp    time.Time `json:"timestamp"`
			InferenceAvg float64   `json:"inference_avg"`
			InferenceMin float64   `json:"inference_min"`
			InferenceMax float64   `json:"inference_max"`
			Ceiling      int       `json:"ceiling"`
		}

		eventRaw := Event{
			Type:         "inferencing_avg",
			Timestamp:    time.Now(),
			InferenceAvg: statsFinal.Avg,
			InferenceMin: statsFinal.Min,
			InferenceMax: statsFinal.Max,
			Ceiling:      1000 / everyNthFrame,
		}
		eventJson, err := json.Marshal(eventRaw)
		if err != nil {
			Log("error", fmt.Sprintf("Error marshalling inference_avg event: %v: Event: %v", err, eventRaw))
			return
		}
		eventHandler("inference_avg", eventJson)
		Log("notice", fmt.Sprintf("Inference avg: %fms, min: %fms, max: %fms", statsFinal.Avg, statsFinal.Min, statsFinal.Max))

		// Clear inferenceTimingLog
		runtimeConfig.InferenceTimingBuffer = make([]InferenceStats, 0)
	}
}

func endMotionEvent() {
	// Log("info", fmt.Sprintf("SINCE_LAST_EVENT: %d GAP: %d", time.Since(runtimeConfig.MotionTriggeredLast), time.Duration(globalConfig.Motion.EventGap)*time.Second))
	Log("info", "MOTION_ENDED")
	runtimeConfig.MotionTriggered = false
	runtimeConfig.MotionMutex.Lock()

	if globalConfig.Notifications.EnablePushoverAlerts { // Send pushover notification
		// // Create gif from snapshots
		gifSliceMutex.Lock()
		CreateGIF(gifSlice, fmt.Sprintf("%s/%s.gif", globalConfig.Video.HiResPath, runtimeConfig.MotionVideo.ID), 100)
		gifSlice = make([]image.RGBA, 0)
		gifSliceMutex.Unlock()

		// Send pushover notification
		err := sendPushoverNotificationGif(globalConfig.Notifications.PushoverUserKey, globalConfig.Notifications.PushoverAppToken, "Motion ended", fmt.Sprintf("%s/%s.gif", globalConfig.Video.HiResPath, runtimeConfig.MotionVideo.ID))
		if err != nil {
			Log("error", fmt.Sprintf("Error sending pushover notification: %v", err))
		}
		// Delete gif
		err = os.Remove(fmt.Sprintf("%s/%s.gif", globalConfig.Video.HiResPath, runtimeConfig.MotionVideo.ID))
		if err != nil {
			Log("error", fmt.Sprintf("Error removing gif file: %v", err))
		}
	}

	// Stop Hi res recording and dump json file as well as clear struct
	runtimeConfig.MotionVideo.MotionEnd = time.Now()
	runtimeConfig.HiResControlChannel <- RecordMsg{Record: false}

	// 更新 MotionVideo.Objects 中的方向信息，使用 lastPositions 中的最新数据
	for i := range runtimeConfig.MotionVideo.Objects {
		obj := &runtimeConfig.MotionVideo.Objects[i]
		// 在 lastPositions 中查找匹配的对象（通过 ID 或位置）
		for _, lastObj := range lastPositions {
			if obj.ID == lastObj.ID || (obj.Class == lastObj.Class && 
				calculateIoU(obj.BBox, lastObj.BBox) > 0.3) {
				// 更新方向和其他跟踪信息
				obj.Direction = lastObj.Direction
				obj.MaxConfidence = lastObj.MaxConfidence
				obj.FrameCount = lastObj.FrameCount
				obj.MovementVector = lastObj.MovementVector
				Log("debug", fmt.Sprintf("Updated object [%s] direction to: %s", obj.ID, obj.Direction))
				break
			}
		}
	}

	// 如果需要转换ts到mp4，则同步执行
	if globalConfig.Video.RecodeTsToMp4 {
		runtimeConfig.MotionVideo.RecodedToMp4 = true
		videoFilePath := filepath.Join(globalConfig.Video.HiResPath, runtimeConfig.MotionVideo.VideoFile)
		
		// 同步执行ts转mp4转换
		mp4FilePath, err := recodeToMP4(videoFilePath)
		if err != nil {
			Log("error", fmt.Sprintf("Error recoding ts file to mp4: %v", err))
		} else {
			// 转换成功，更新VideoFile为mp4文件名
			runtimeConfig.MotionVideo.VideoFile = filepath.Base(mp4FilePath)
			Log("info", fmt.Sprintf("Successfully converted %s to %s", videoFilePath, mp4FilePath))
			
			// 删除原始ts文件
			err = os.Remove(videoFilePath)
			if err != nil {
				Log("error", fmt.Sprintf("Error removing ts file: %v", err))
			}
		}
	}

	// 转换完成后，重新生成meta.json文件
	jsonData, err := json.Marshal(runtimeConfig.MotionVideo)
	if err != nil {
		Log("error", fmt.Sprintf("Error marshalling metadata: %v", err))
	}

	err = os.WriteFile(filepath.Join(globalConfig.Video.HiResPath, fmt.Sprintf("meta_%s.json", runtimeConfig.MotionVideo.ID)), jsonData, 0644)
	if err != nil {
		Log("error", fmt.Sprintf("Error writing metadata file: %v", err))
	}

	// 在转换和meta.json更新完成后，触发motion_end事件
	eventRaw := Event{
		Type:                "motion_ended",
		Timestamp:           time.Now(),
		MotionTriggeredLast: runtimeConfig.MotionTriggeredLast,
		ID:                  runtimeConfig.MotionVideo.ID,
		MotionStart:         runtimeConfig.MotionVideo.MotionStart,
		MotionEnd:           runtimeConfig.MotionVideo.MotionEnd,
		Objects:             runtimeConfig.MotionVideo.Objects,
		RecodedToMp4:        runtimeConfig.MotionVideo.RecodedToMp4,
		Snapshots:           runtimeConfig.MotionVideo.Snapshots,
		VideoFile:           runtimeConfig.MotionVideo.VideoFile,
		CameraName:          runtimeConfig.MotionVideo.CameraName,
		MetadataPath:        filepath.Join(globalConfig.Video.HiResPath, fmt.Sprintf("meta_%s.json", runtimeConfig.MotionVideo.ID)),
	}
	eventJson, err := json.Marshal(eventRaw)
	if err != nil {
		Log("error", fmt.Sprintf("Error marshalling motion_ended event: %v", err))
		return
	}
	eventHandler("motion_end", eventJson)

	// 	// Clear the whole runtimeConfig.MotionVideo struct
	runtimeConfig.MotionVideo = VideoMetadata{}

	runtimeConfig.MotionMutex.Unlock()
}

func sendPushoverNotification(userKey string, appToken string, msg string, img *image.RGBA) error {
	// Convert the image to JPEG format
	var imgBuffer bytes.Buffer
	if err := jpeg.Encode(&imgBuffer, img, nil); err != nil {
		return fmt.Errorf("error encoding image: %v", err)
	}

	// Create a new HTTP request
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// Set up form fields
	if err := w.WriteField("token", appToken); err != nil {
		return fmt.Errorf("WriteField Error: %v", err)
	}
	if err := w.WriteField("user", userKey); err != nil {
		return fmt.Errorf("WriteField Error: %v", err)
	}
	if err := w.WriteField("message", msg); err != nil {
		return fmt.Errorf("WriteField Error: %v", err)
	}

	// Attach the in-memory image as an attachment
	fw, err := w.CreateFormFile("attachment", "image.jpg")
	if err != nil {
		return fmt.Errorf("CreateFormFile Error: %v", err)
	}
	if _, err := io.Copy(fw, &imgBuffer); err != nil {
		return fmt.Errorf("copy File Error: %v", err)
	}

	// Close the writer
	w.Close()

	// Create a HTTP request to Pushover API
	req, err := http.NewRequest("POST", "https://api.pushover.net/1/messages.json", &b)
	if err != nil {
		return fmt.Errorf("NewRequest Error: %v", err)
	}

	// Set the content type, this will include the boundary.
	req.Header.Set("Content-Type", w.FormDataContentType())

	// Execute the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error executing request: %v", err)
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Non-OK HTTP status: %s", resp.Status)
	}

	return nil
}

// CreateGIF creates a GIF file from a slice of *image.RGBA images
func CreateGIF(images []image.RGBA, outputPath string, delay int) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	anim := &gif.GIF{}
	for _, srcImg := range images {
		// Convert image.RGBA to *image.Paletted
		bounds := srcImg.Bounds()
		palettedImage := image.NewPaletted(bounds, palette.Plan9)
		draw.Draw(palettedImage, palettedImage.Rect, &srcImg, bounds.Min, draw.Over)

		anim.Image = append(anim.Image, palettedImage)
		anim.Delay = append(anim.Delay, delay)
	}

	return gif.EncodeAll(outFile, anim)
}

func sendPushoverNotificationGif(userKey string, appToken string, msg string, gifPath string) error {
	// Open the GIF file
	file, err := os.Open(gifPath)
	if err != nil {
		return fmt.Errorf("Error opening GIF file: %v", err)
	}
	defer file.Close()

	// Create a new HTTP request
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// Set up form fields
	if err := w.WriteField("token", appToken); err != nil {
		return fmt.Errorf("WriteField Error: %v", err)
	}
	if err := w.WriteField("user", userKey); err != nil {
		return fmt.Errorf("WriteField Error: %v", err)
	}
	if err := w.WriteField("message", msg); err != nil {
		return fmt.Errorf("WriteField Error: %v", err)
	}

	// Attach the GIF file as an attachment
	fw, err := w.CreateFormFile("attachment", "image.gif")
	if err != nil {
		return fmt.Errorf("CreateFormFile Error: %v", err)
	}
	if _, err := io.Copy(fw, file); err != nil {
		return fmt.Errorf("Copy File Error: %v", err)
	}

	// Close the writer
	w.Close()

	// Create a HTTP request to Pushover API
	req, err := http.NewRequest("POST", "https://api.pushover.net/1/messages.json", &b)
	if err != nil {
		return fmt.Errorf("NewRequest Error: %v", err)
	}

	// Set the content type, this will include the boundary.
	req.Header.Set("Content-Type", w.FormDataContentType())

	// Execute the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Error executing request: %v", err)
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Non-OK HTTP status: %s", resp.Status)
	}

	return nil
}