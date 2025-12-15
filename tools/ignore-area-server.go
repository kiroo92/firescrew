package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

//go:embed ignore-area-editor.html
var content embed.FS

func main() {
	port := flag.String("port", "8080", "服务端口")
	flag.Parse()

	// 提供编辑器页面
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 设置 CORS 头，允许跨域访问视频流
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		data, err := content.ReadFile("ignore-area-editor.html")
		if err != nil {
			http.Error(w, "无法加载编辑器", http.StatusInternalServerError)
			log.Printf("错误: %v", err)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	// 代理视频流，解决跨域问题
	http.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		// 从路径中提取目标URL
		targetURL := strings.TrimPrefix(r.URL.Path, "/stream/")
		if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
			targetURL = "http://" + targetURL
		}

		log.Printf("代理请求: %s", targetURL)

		// 发送请求到目标服务器
		resp, err := http.Get(targetURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("无法连接到视频流: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// 复制响应头
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// 设置CORS
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(resp.StatusCode)

		// 复制响应体
		io.Copy(w, resp.Body)
	})

	addr := ":" + *port
	fmt.Printf("\n🎯 IgnoreAreasClasses 区域编辑器已启动\n")
	fmt.Printf("📍 访问地址: http://localhost%s\n", addr)
	fmt.Printf("📝 在浏览器中打开上述地址开始编辑\n\n")

	log.Fatal(http.ListenAndServe(addr, nil))
}
