// ============================================================================
// 小米摄像头录像合并工具（Xiaomi Camera Video Merger）
// ----------------------------------------------------------------------------
// 【工具用途】
//   小米摄像头将录像以“约 19 分钟一段”的 .mp4 小分段保存在群晖 NAS 的
//   CCTV 共享文件夹中（形如 00_20260922091818_20260922093706.mp4）。
//   本工具把这些零散分段按【摄像头/通道 + 时间顺序每 10 个左右】合并成
//   一个完整视频，并按 年/月/日 目录自动归档，可选的删除已合并
//   的源文件以释放空间。
//
// 【v3.0 分组规则升级（相对 v2.0）】
//   1. 不再按“小时”归组，改为单元内按时间顺序每 GROUP_SIZE（默认10）个
//      文件一组合并，满足“10 个左右一合并”；
//   2. 最后一组不足 MIN_GROUP_SIZE（默认8）个文件时不合并，等待积攒；
//   3. 输出目录 merged/<年>/<月>/<日>/，文件名 <组开始时间>_<单元短名>.mp4。
//
// 【v2.0 保留能力（相对上游 v1.x）】
//   1. 自适应目录结构：兼容“摄像头目录下直接平铺文件”与
//      “摄像头目录/通道子目录（0=主码流、1=副码流）”两种真实结构；
//   2. 跳过系统目录（#recycle 回收站、. 开头目录）、输出目录、日志目录；
//   3. 从文件名提取首个 14 位数字串作为开始时间，更稳健；
//   4. 跳过仍在写入的文件（-min_age_min 默认 30 分钟）与当前小时
//      （-skip_current 默认 true），避免合并到未写完的文件；
//   5. 增加结束汇总统计与 MAX_MERGE 边界修复。
//
// 【运行方式】
//   本机直接运行：  xiaomi_camera_merge.exe -path <视频根目录> [参数]
//   Docker 运行：   见同目录 Dockerfile / docker-compose.yml（NAS 部署用）
//   群晖定时任务：  见 synology-task-scheduler.sh / 操作文档
//
// 【环境变量（Docker 部署时与命令行参数等价）】
//   DELETE_SUCCESS / MAX_MERGE / VIDEO_EXT / OUTPUT_DIR / MIN_AGE_MIN / SKIP_CURRENT / GROUP_SIZE / MIN_GROUP_SIZE（v3.0 新增）
// ============================================================================
package main

import (
	"errors"
	"flag"
	"fmt"
	"mi_camera_merge/lib/log" // 自定义日志库（输出到 视频根目录/xiaomi-video-merge-log/）
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 全局常量与变量
// ---------------------------------------------------------------------------
const (
	VERSION     = "v3.0"                                                      // 工具版本号
	AUTHOR      = "开发者：红烧猎人(联系作者:https://blog.enianteam.com/u/sun/content/11)" // 原作者信息
	ADDRESS     = "工具开源地址和使用教程:https://github.com/hslr-s/xiaomi-camera-merge"  // 上游开源地址
	ERROR_EMPTY = "empty group"                                               // 空分组错误标识
)

// Logger 全局日志句柄，由 main 初始化，用于同时输出到终端与日志文件
var Logger *log.Log

// videoFile 表示一个待合并的源视频文件
type videoFile struct {
	path    string    // 文件的绝对路径
	start   string    // 从文件名解析出的开始时间，格式 YYYYMMDDHHMMSS（14 位数字）
	modTime time.Time // 文件修改时间，用于判断是否仍在写入
}

// ---------------------------------------------------------------------------
// main：程序入口
//   流程：解析参数/环境变量 → 校验环境 → 初始化日志 → 输出配置 → 执行合并
// ---------------------------------------------------------------------------
func main() {
	// ---- 1. 定义命令行参数（默认值 = Docker 容器内友好默认值） ----
	var path string        // 视频根目录：该目录下的每个子目录视为一个摄像头
	var delete bool        // 是否在合并成功后删除已合并的源文件
	var maxMerge string    // 单次运行最多合并多少组（0 = 不限制）
	var ext string         // 待合并文件的扩展名
	var outName string     // 合并输出目录名（位于视频根目录之下）
	var minAgeMin int      // 只处理修改时间早于该分钟数的文件（防止合并到写入中的文件）
	var skipCurrent bool   // 是否跳过“开始时间位于当前小时”的文件（摄像头可能仍在写当前小时）
	var groupSize int      // 每组合并多少个分段（v3.0：按时间顺序每 N 个一组，默认 10）
	var minGroupSize int   // “10个左右”的下限：最后一组达到该数量也合并，否则等待下次（默认 8）
	flag.StringVar(&path, "path", "./", "视频的保存目录（该目录下为各摄像头文件夹）")
	flag.BoolVar(&delete, "delete", false, "合并成功后删除已合并的源视频")
	flag.StringVar(&maxMerge, "max_merge", "0", "默认:0 不限制. 每次最大合并组数，视频量巨多时分段执行")
	flag.StringVar(&ext, "ext", "mp4", "待合并的视频扩展名，默认 mp4")
	flag.StringVar(&outName, "out", "merged", "合并输出目录名（位于视频根目录下）")
	flag.IntVar(&minAgeMin, "min_age_min", 30, "只处理修改时间早于该分钟数的文件，避免误合并正在写入的视频")
	flag.BoolVar(&skipCurrent, "skip_current", true, "跳过开始时间处于当前小时内的文件（摄像头可能仍在写入）")
	flag.IntVar(&groupSize, "group_size", 10, "每组合并的分段数量（v3.0 按时间顺序每 N 个一组）")
	flag.IntVar(&minGroupSize, "min_group_size", 8, "最后一组达到该数量也合并，不足则等待下次")
	flag.Parse()

	// ---- 2. 读取 Docker 环境变量（环境变量优先于命令行默认值） ----
	// 说明：在群晖 Container Manager 中通过 docker-compose.yml 的 environment
	// 注入，这样无需在命令行写一长串参数。
	if envDelete := os.Getenv("DELETE_SUCCESS"); envDelete == "true" {
		delete = true // 只要环境变量为字符串 "true" 即开启删除
	}
	if envMaxMerge := os.Getenv("MAX_MERGE"); envMaxMerge != "" {
		maxMerge = envMaxMerge // "0" 表示不限制
	}
	if envExt := os.Getenv("VIDEO_EXT"); envExt != "" {
		ext = envExt
	}
	if envOut := os.Getenv("OUTPUT_DIR"); envOut != "" {
		outName = envOut
	}
	if v, err := strconv.Atoi(os.Getenv("MIN_AGE_MIN")); err == nil {
		minAgeMin = v // 非法数字时忽略，保留默认值
	}
	if v, err := strconv.ParseBool(os.Getenv("SKIP_CURRENT")); err == nil {
		skipCurrent = v
	}
	if v, err := strconv.Atoi(os.Getenv("GROUP_SIZE")); err == nil && v > 0 {
		groupSize = v
	}
	if v, err := strconv.Atoi(os.Getenv("MIN_GROUP_SIZE")); err == nil && v > 0 {
		minGroupSize = v
	}
	if groupSize < 1 {
		groupSize = 10
	}
	if minGroupSize < 1 || minGroupSize > groupSize {
		minGroupSize = groupSize
	}

	// ---- 3. 打印版本信息 ----
	PrintAppInfo()
	fmt.Println("正在初始化，请稍后")
	fmt.Println("===========================")

	// ---- 4. 环境校验 ----
	// 4.1 检查 ffmpeg 是否存在（合并动作依赖 ffmpeg 的 concat 滤镜）
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Println("错误中断执行：未找到 ffmpeg 程序，请先安装 ffmpeg 或使用 docker 镜像运行。")
		return
	}
	// 4.2 检查视频根目录是否存在且为文件夹
	if stat, err := os.Stat(path); err != nil || !stat.IsDir() {
		fmt.Println("错误中断执行：视频目录不存在或不是文件夹：", path)
		return
	}

	// ---- 5. 初始化日志 ----
	// 日志文件按运行时刻命名：xiaomi-video-merge-log/<时间戳>.log
	currentTime := time.Now()
	formattedTime := currentTime.Format("20060102_150405") // 如 20260922_110930
	logPath := filepath.Join(path, "xiaomi-video-merge-log")
	if err := os.MkdirAll(logPath, 0777); err != nil {
		fmt.Println("错误中断执行：日志文件夹创建失败:", err.Error())
		return
	}
	Logger = log.NewLog(filepath.Join(logPath, formattedTime+".log"))

	// ---- 6. 输出本次运行配置（同时写入日志） ----
	LoggerAppInfo()
	EchoLog("=== 本次运行配置 ===")
	EchoLog("视频根目录：", path)
	EchoLog("合并后是否删除源视频：", delete)
	EchoLog("最大合并组数（0=不限制）：", maxMerge)
	EchoLog("待合并视频扩展名：", ext)
	EchoLog("输出目录：", filepath.Join(path, outName))
	EchoLog("最小文件年龄(分钟)：", minAgeMin)
	EchoLog("跳过当前小时：", skipCurrent)
	EchoLog("每组文件数（默认10个左右）：", groupSize)
	EchoLog("最后一组下限文件数：", minGroupSize)

	EchoLog("合并的时间取决于视频数量和尺寸，请尽量不要在合并过程中，修改操作目录的权限、删除目录等操作")
	EchoLog("=== 合并开始 ===")

	// ---- 7. 执行合并主逻辑，返回 成功/失败/跳过 的组数 ----
	merged, failed, skipped := StartMerge(path, delete, maxMerge, ext, outName, minAgeMin, skipCurrent, groupSize, minGroupSize)

	// ---- 8. 输出总结 ----
	EchoLog("=== 合并结束 ===")
	EchoLog(fmt.Sprintf("本次共处理 %d 组：成功 %d 组，失败 %d 组，跳过 %d 组（已存在输出/当前小时/无可合并文件/文件名不含时间戳/不足一组等待积攒）", merged+failed+skipped, merged, failed, skipped))
	EchoLog("请注意！结束不代表全部成功了，具体请向上查看详情")
}

// ---------------------------------------------------------------------------
// 数据结构
// ---------------------------------------------------------------------------

// group 表示一个待合并的组：某合并单元目录下、按时间顺序每 N 个文件一组
// 例如：XiaomiCamera_00_xxx 下按时间排序后第 1~10 个文件
type group struct {
	unitDir string      // 文件所在目录（合并单元，如 摄像头根目录 或 摄像头/通道子目录）
	start   string      // 组开始时间，格式 YYYYMMDDHHMMSS（组内第一个文件的开始时间，用于命名）
	files   []videoFile // 组内文件（已按开始时间升序排序）
}

// ---------------------------------------------------------------------------
// StartMergeX：核心合并流程
//   1. 解析 max_merge
//   2. 计算“当前小时”与“最小文件年龄”阈值
//   3. 扫描根目录，收集合并单元（摄像头/通道）
//   4. 遍历单元，按 文件名开始时间的小时 分组
//   5. 组内排序后逐组合并（或跳过已存在输出）
//   6. 统计并返回 成功/失败/跳过 组数
// ---------------------------------------------------------------------------
func StartMerge(path string, deleteSrc bool, maxMerge string, ext string, outName string, minAgeMin int, skipCurrent bool, groupSize int, minGroupSize int) (int, int, int) {
	// 解析 max_merge："0" 或非法值 → 不限制
	maxMergeInt := 0
	if v, err := strconv.Atoi(maxMerge); err == nil {
		maxMergeInt = v
	}

	// 当前时间基准
	now := time.Now()
	curHour := now.Format("20060102") + now.Format("15") // 形如 2026092211（日期+小时）
	cutoff := now.Add(-time.Duration(minAgeMin) * time.Minute) // 修改时间早于 cutoff 的文件才处理

	// 读取根目录条目
	entries, err := os.ReadDir(path)
	if err != nil {
		EchoLog("错误：读取目录失败：", path, "，错误原因：", err)
		return 0, 0, 0
	}

	// ---- 收集合并单元 ----
	// 根目录下的每个子目录都视为一个摄像头；跳过：
	//   - 以 # 开头的目录（如 #recycle 回收站）
	//   - 以 . 开头的目录（系统隐藏目录）
	//   - 输出目录（merged）与日志目录（xiaomi-video-merge-log）
	var units []string
	for _, e := range entries {
		if !e.IsDir() {
			continue // 只关心子目录
		}
		name := e.Name()
		if strings.HasPrefix(name, "#") || strings.HasPrefix(name, ".") {
			continue
		}
		if name == outName || name == "xiaomi-video-merge-log" {
			continue
		}
		units = append(units, filepath.Join(path, name))
	}
	sort.Strings(units) // 排序保证处理顺序稳定

	// ---- 组装分组 ----
	// 每个合并单元可能有两种结构：
	//   A. 平铺：摄像头目录下直接就是 .mp4 文件（XiaomiCamera_00_xxx）
	//   B. 通道：摄像头目录下有 0/1 等通道子目录（XiaomiCamera_01_xxx/0、/1）
	// v3.0：不再按小时归组，先把单元内所有文件收集起来，
	// 按开始时间升序排序后每 GROUP_SIZE 个文件切成一组
	unitFiles := make(map[string][]videoFile)

	// unitBase：把单元目录转成相对根目录的路径（用于输出归档路径）
	unitBase := func(unitDir string) string {
		rel, _ := filepath.Rel(path, unitDir)
		return rel
	}

	// walkUnit：递归遍历一个单元目录，把符合条件的文件收集起来
	walkUnit := func(unitDir string) {
		filepath.Walk(unitDir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // 单个文件读取失败不阻断整体
			}
			if info.IsDir() {
				// 遇到输出目录/日志目录则整棵跳过（它们会随着运行不断变大，绝不能扫进来）
				if info.Name() == outName || info.Name() == "xiaomi-video-merge-log" {
					return filepath.SkipDir
				}
				return nil
			}
			// 只处理指定扩展名的文件（不区分大小写）
			if !strings.EqualFold(filepath.Ext(p), "."+ext) {
				return nil
			}
			// 跳过修改时间在阈值之后的文件（很可能正在写入）
			if info.ModTime().After(cutoff) {
				return nil
			}
			// 从文件名解析开始时间；解析不到则跳过并记录
			start := parseStartTime(info.Name())
			if start == "" {
				EchoLog("跳过：文件名无法解析开始时间：", p)
				return nil
			}
			// 跳过当前小时（摄像头可能仍在写入该小时，防止把未写完的分段并入）
			if skipCurrent {
				hour := start[0:8] + start[8:10]
				if hour == curHour {
					return nil
				}
			}
			unitFiles[unitDir] = append(unitFiles[unitDir], videoFile{path: p, start: start, modTime: info.ModTime()})
			return nil
		})
	}

	// 对每个单元判断结构类型后遍历
	for _, u := range units {
		// 若单元目录下直接存在 *.mp4，说明是平铺结构 → 整个目录一个单元
		direct, _ := filepath.Glob(filepath.Join(u, "*."+ext))
		if len(direct) > 0 {
			walkUnit(u)
		} else {
			// 否则按通道子目录细分（跳过 # / . 开头的子目录）
			subs, _ := os.ReadDir(u)
			for _, s := range subs {
				if s.IsDir() && !strings.HasPrefix(s.Name(), "#") && !strings.HasPrefix(s.Name(), ".") {
					walkUnit(filepath.Join(u, s.Name()))
				}
			}
		}
	}

	// ---- 分组（v3.0）：单元内按时间顺序每 N 个文件一组 ----
	var groups []*group
	for u, files := range unitFiles {
		sort.Slice(files, func(i, j int) bool { return files[i].start < files[j].start })
		for i := 0; i < len(files); i += groupSize {
			end := i + groupSize
			if end > len(files) {
				end = len(files)
			}
			chunk := files[i:end]
			// 最后一组不足 MIN_GROUP_SIZE 个：跳过，等待积攒
			if len(chunk) < minGroupSize {
				EchoLog("跳过：单元 ", unitBase(u), " 剩余 ", len(chunk), " 个文件不足一组（下限 ", minGroupSize, "），等待下次运行积攒")
				break
			}
			groups = append(groups, &group{unitDir: u, start: chunk[0].start, files: chunk})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].start != groups[j].start {
			return groups[i].start < groups[j].start
		}
		return groups[i].unitDir < groups[j].unitDir
	})

	// ---- 逐组处理 ----
	merged, failed, skipped := 0, 0, 0
	for _, g := range groups {
		if len(g.files) == 0 {
			continue
		}

		// 输出路径：merged/<年>/<月>/<日>/<开始时间>_<单元短名>.mp4
		rel := unitBase(g.unitDir)
		outDir := filepath.Join(path, outName, g.start[0:4], g.start[4:6], g.start[6:8])
		outFile := filepath.Join(outDir, g.start+"_"+unitShort(rel)+".mp4")

		// 三种结果：输出已存在（跳过）/ 合并失败 / 合并成功
		if _, err := os.Stat(outFile); err == nil {
			skipped++
			EchoLog("跳过：输出已存在：", outFile, "（如需重新合并请先删除该输出文件）")
		} else if _, err := MergeGroup(g, outDir, outFile); err != nil {
			failed++
			EchoLog("错误：组 ", rel, " 开始 ", g.start, " 合并失败。错误原因：", err)
		} else {
			merged++
			EchoLog("组 ", rel, " 开始 ", g.start, " 合并成功：", outFile)
			// 可选：删除已成功合并的源文件（仅当 deleteSrc 为 true）
			if deleteSrc {
				del, delErr := deleteMergedFiles(g.files)
				if delErr != nil {
					EchoLog("警告：源文件删除不完整：", delErr)
				}
				EchoLog(fmt.Sprintf("已删除 %d/%d 个源文件", del, len(g.files)))
			}
		}

		EchoLog("==========", merged+failed)
		// MAX_MERGE 限制：达到后提前结束，剩余留到下次运行（适合超大积压分批处理）
		if maxMergeInt != 0 && merged+failed >= maxMergeInt {
			EchoLog("已达到最大合并组数：", maxMergeInt, "，本次停止。剩余目录下次运行再合并。")
			break
		}
	}
	return merged, failed, skipped
}

// ---------------------------------------------------------------------------
// parseStartTime：从文件名中提取开始时间
//   小米摄像头文件命名：00_20260922091818_20260922093706.mp4
//   规则：扫描文件名，取第一个连续的 14 位数字串，即为 YYYYMMDDHHMMSS
// ---------------------------------------------------------------------------
func parseStartTime(name string) string {
	digits := ""
	for _, r := range name {
		if r >= '0' && r <= '9' {
			digits += string(r)
			if len(digits) == 14 {
				return digits // 找到 14 位连续数字即返回
			}
		} else {
			digits = "" // 遇到非数字则重新计数
		}
	}
	return "" // 未找到合法的 14 位时间戳
}

// unitShort：把单元相对路径压缩成输出文件名的单元短名（平铺 00、通道 01_0/01_1）
func unitShort(rel string) string {
	parts := strings.Split(rel, string(filepath.Separator))
	first := parts[0]
	idx := strings.Index(first, "_")
	if idx >= 0 && idx+3 <= len(first) {
		short := first[idx+1 : idx+3]
		if len(parts) > 1 {
			short += "_" + parts[1]
		}
		return short
	}
	return strings.ReplaceAll(rel, string(filepath.Separator), "_")
}


// ---------------------------------------------------------------------------
// MergeGroup：把一组文件按时间顺序合并为一个 mp4
//   方案：生成 ffmpeg concat 列表文件 files.txt，
//         使用 `-f concat -safe 0 -i files.txt -c copy` 流复制合并
//         （不重新编码，速度快、不损失画质），
//         先输出到单元目录下的临时文件，成功后原子重命名到最终路径，
//         避免合并失败时留下残缺的目标文件。
// ---------------------------------------------------------------------------
func MergeGroup(g *group, outDir, outFile string) (string, error) {
	if len(g.files) == 0 {
		return "", errors.New(ERROR_EMPTY) // 空组直接报错
	}

	// ---- 生成 concat 列表（路径相对单元目录，避免绝对路径过长） ----
	fileContent := ""
	for _, f := range g.files {
		relPath, err := filepath.Rel(g.unitDir, f.path)
		if err != nil {
			return "", err
		}
		// 对单引号做转义（文件名含单引号时安全）
		escaped := strings.ReplaceAll(relPath, "'", `'\''`)
		fileContent += "file '" + escaped + "'\n"
	}

	// 写 files.txt 到单元目录（ffmpeg concat 从该文件读取待合并列表）
	filesTxtPath := filepath.Join(g.unitDir, "files.txt")
	if err := SaveFileList(filesTxtPath, fileContent); err != nil {
		return "", errors.New("生成临时文件出错：" + err.Error())
	}
	defer os.Remove(filesTxtPath) // 合并结束后清理临时列表

	// 创建输出目录（merged/摄像头/年/月/日）
	if err := os.MkdirAll(outDir, 0777); err != nil {
		return "", errors.New("创建输出目录失败：" + err.Error())
	}

	// ---- 执行 ffmpeg 合并 ----
	// 先输出到单元目录下临时文件 _merge_tmp_<小时>.mp4
	tmpOut := filepath.Join(g.unitDir, "_merge_tmp_"+g.start+".mp4")
	os.Remove(tmpOut)
	EchoLog("执行 ffmpeg 指令，待合并文件数：", len(g.files))
	if err := ExecCommand(g.unitDir, "ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", "files.txt", "-c", "copy", "_merge_tmp_"+g.start+".mp4"); err != nil {
		os.Remove(tmpOut) // 失败时清理临时文件
		return "", err
	}

	// 合并成功后，把临时文件移动到最终归档路径（原子操作）
	if err := os.Rename(tmpOut, outFile); err != nil {
		os.Remove(tmpOut)
		return "", errors.New("转移合并后的视频出错：" + err.Error())
	}
	return outFile, nil
}

// ---------------------------------------------------------------------------
// deleteMergedFiles：删除已成功合并的源文件
//   返回实际删除数量；若有部分删除失败，返回第一个错误（不中断继续删）
// ---------------------------------------------------------------------------
func deleteMergedFiles(files []videoFile) (int, error) {
	del := 0
	var firstErr error
	for _, f := range files {
		if err := os.Remove(f.path); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		del++
	}
	return del, firstErr
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

// SaveFileList：把内容写入文件（用于生成 ffmpeg concat 列表）
func SaveFileList(filePath, content string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := fmt.Fprintln(file, content); err != nil {
		return err
	}
	return file.Sync() // 同步落盘，确保 ffmpeg 能读到完整内容
}

// ExecCommand：在指定工作目录执行命令，并回显命令与输出（含错误输出）
func ExecCommand(workpath, name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Dir = workpath
	EchoLog("执行命令：", cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		EchoLog("指令执行错误：\n", err)
		if string(out) != "" {
			EchoLog("ffmpeg 输出：\n", string(out))
		}
		return err
	}
	if string(out) != "" {
		EchoLog("指令执行结果输出：\n", string(out))
	}
	return nil
}

// PrintAppInfo：向终端打印程序标题与说明
func PrintAppInfo() {
	fmt.Println("===========================")
	fmt.Println("小米摄像头视频合并工具", VERSION)
	fmt.Println("===========================")
	fmt.Println(AUTHOR)
	fmt.Println(ADDRESS)
	fmt.Println("===========================")
	fmt.Println("v3.0 自适应结构：兼容小米摄像头 NAS 存储的「摄像头/通道目录 + 时间段命名文件」结构，")
	fmt.Println("按文件名开始时间排序，每 10 个左右文件一组合并为一个 mp4，并按 年/月/日 目录归档")
	fmt.Println("===========================")
}

// LoggerAppInfo：向日志文件写入程序标题与说明
func LoggerAppInfo() {
	Logger.WriteContent("===========================")
	Logger.WriteContent("小米摄像头视频合并工具", VERSION)
	Logger.WriteContent("===========================")
	Logger.WriteContent(AUTHOR)
	Logger.WriteContent(ADDRESS)
	Logger.WriteContent("===========================")
}

// EchoLog：同时输出到终端与日志文件（运行全程的“双写”出口）
func EchoLog(a ...any) {
	fmt.Println(a...)
	if err := Logger.WriteContent(a...); err != nil {
		fmt.Println("日志生成错误:", err.Error())
	}
}
