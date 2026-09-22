// ============================================================================
// lib/log/log.go —— 简易日志库
// ----------------------------------------------------------------------------
// 用途：把合并过程的输出同时写入 NAS 上的日志文件
//       （视频根目录/xiaomi-video-merge-log/<运行时间戳>.log），
//       便于事后排查。功能等价于标准库 log 的一个薄封装。
// ============================================================================
package log

import (
	"log"
	"os"
)

// Log 表示一个日志文件的句柄封装
type Log struct {
	Path string // 日志文件完整路径
}

// NewLog 创建日志对象，仅记录路径，不立即打开文件
func NewLog(path string) *Log {
	return &Log{
		Path: path,
	}
}

// WriteContent 把若干内容写入日志文件（自动创建/追加）
// 每次调用都重新打开文件并追加一行，带日期时间前缀
func (l *Log) WriteContent(content ...any) error {
	// 以“创建或追加”模式打开日志文件（不存在则创建）
	logFile, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	defer logFile.Close()

	// 将标准库 log 的输出重定向到该文件，并加上 日期+时间 前缀
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime)
	log.Println(content...)
	return nil
}
