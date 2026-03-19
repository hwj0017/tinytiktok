package main

import (
	"context"
	worker "feedsystem_video_go/internal/worker"
	"fmt"
	"os"
)

func main() {
	videoPath := "/home/hwj/video/3eb59e29b12a4cee23975530b391d32b.mp4"
	outputPath := "output.wav"

	fmt.Println("正在提取音频...")

	// 1. 调用你之前的函数获取 []byte
	audioData, err := worker.ExtractAudio(context.Background(), videoPath)
	if err != nil {
		fmt.Printf("提取失败: %v\n", err)
		return
	}

	// 2. 将字节数组写入文件 (权限 0644 表示普通读写权限)
	err = os.WriteFile(outputPath, audioData, 0644)
	if err != nil {
		fmt.Printf("保存文件失败: %v\n", err)
		return
	}

	fmt.Printf("音频提取成功，已保存至: %s\n", outputPath)
}
