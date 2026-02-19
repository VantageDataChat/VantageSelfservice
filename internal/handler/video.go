package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"askflow/internal/video"
)

// --- Video dependency check / auto-setup handlers ---

// HandleVideoCheckDeps checks whether FFmpeg and RapidSpeech are available.
func HandleVideoCheckDeps(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		// Require admin session
		_, _, err := GetAdminSession(app, r)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, err.Error())
			return
		}
		cfg := app.configManager.Get()
		if cfg == nil {
			WriteJSON(w, http.StatusOK, video.DepsCheckResult{})
			return
		}
		vp := video.NewParser(cfg.Video)
		result := vp.CheckDependencies()
		WriteJSON(w, http.StatusOK, result)
	}
}

// HandleValidateRapidSpeech validates RapidSpeech configuration paths before saving.
func HandleValidateRapidSpeech(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		_, _, err := GetAdminSession(app, r)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, err.Error())
			return
		}
		var req struct {
			RapidSpeechPath  string `json:"rapidspeech_path"`
			RapidSpeechModel string `json:"rapidspeech_model"`
		}
		if err := ReadJSONBody(r, &req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		vp := &video.Parser{
			RapidSpeechPath:  req.RapidSpeechPath,
			RapidSpeechModel: req.RapidSpeechModel,
		}
		validationErrors := vp.ValidateRapidSpeechConfig()
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"valid":  len(validationErrors) == 0,
			"errors": validationErrors,
		})
	}
}

// HandleVideoAutoSetup performs automatic installation of FFmpeg and RapidSpeech.
// It streams progress via Server-Sent Events (SSE).
// Steps: install system deps (git/gcc/cmake) → install ffmpeg → clone & build RapidSpeech → download model → configure paths.
func HandleVideoAutoSetup(app *App) http.HandlerFunc {
	var setupRunning int32 // atomic guard: 0 = idle, 1 = running
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		// Only Linux is supported for auto-setup
		if runtime.GOOS != "linux" {
			WriteError(w, http.StatusBadRequest, "auto-setup is only supported on Linux")
			return
		}
		_, role, err := GetAdminSession(app, r)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if role != "super_admin" {
			WriteError(w, http.StatusForbidden, "仅超级管理员可执行自动配置")
			return
		}

		// Check if running as root (required for apt-get install)
		if os.Getuid() != 0 {
			WriteError(w, http.StatusForbidden, "自动配置需要以管理员权限（root）运行服务，请使用 sudo 启动程序后重试")
			return
		}

		// Prevent concurrent auto-setup runs
		if !atomic.CompareAndSwapInt32(&setupRunning, 0, 1) {
			WriteError(w, http.StatusConflict, "自动配置正在进行中，请等待完成后再试")
			return
		}
		defer atomic.StoreInt32(&setupRunning, 0)

		// SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			WriteError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		sendSSE := func(eventType, message string, progress int) {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			data := map[string]interface{}{
				"type":     eventType,
				"message":  message,
				"progress": progress,
			}
			jsonData, _ := json.Marshal(data)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			flusher.Flush()
		}

		// Helper: run a shell command, stream output lines via SSE
		runCmd := func(ctx context.Context, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return err
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				return err
			}
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
			for scanner.Scan() {
				select {
				case <-ctx.Done():
					cmd.Process.Kill()
					return ctx.Err()
				default:
				}
				line := scanner.Text()
				if len(line) > 500 {
					line = line[:500] + "..."
				}
				sendSSE("log", line, -1)
			}
			return cmd.Wait()
		}

		ctx := r.Context()

		// Determine install base directory: use executable's directory as base
		exePath, _ := os.Executable()
		installBase := filepath.Dir(exePath)
		if installBase == "" || installBase == "." {
			installBase = "/opt/askflow"
		}
		baseDir := filepath.Join(installBase, "rapidspeech-build")
		modelDir := filepath.Join(installBase, "rapidspeech-models")

		// Detect region: use HEAD request to avoid downloading response body
		isChinaRegion := false
		{
			req, _ := http.NewRequestWithContext(ctx, http.MethodHead, "https://www.modelscope.cn/api/v1/models", nil)
			if req != nil {
				client := &http.Client{Timeout: 3 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode < 500 {
						isChinaRegion = true
					}
				}
			}
		}

		// ── Step 1: Install system dependencies ──
		sendSSE("step", "正在安装系统依赖 (git, gcc, g++, cmake, make)...", 5)
		if err := runCmd(ctx, "apt-get", "update", "-y"); err != nil {
			sendSSE("error", fmt.Sprintf("apt-get update 失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		if err := runCmd(ctx, "apt-get", "install", "-y",
			"git", "gcc", "g++", "cmake", "make", "wget", "curl",
			"pkg-config", "libssl-dev"); err != nil {
			sendSSE("error", fmt.Sprintf("安装系统依赖失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		sendSSE("step", "系统依赖安装完成 ✓", 15)

		// ── Step 2: Install FFmpeg ──
		sendSSE("step", "正在安装 FFmpeg...", 20)
		if err := runCmd(ctx, "apt-get", "install", "-y", "ffmpeg"); err != nil {
			sendSSE("error", fmt.Sprintf("FFmpeg 安装失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		// Find ffmpeg path
		ffmpegPath := "/usr/bin/ffmpeg"
		if _, err := os.Stat(ffmpegPath); err != nil {
			// Try which
			out, err2 := exec.Command("which", "ffmpeg").Output()
			if err2 != nil {
				sendSSE("error", "FFmpeg 安装后未找到可执行文件", -1)
				sendSSE("done", "安装失败", -1)
				return
			}
			ffmpegPath = strings.TrimSpace(string(out))
		}
		sendSSE("step", fmt.Sprintf("FFmpeg 安装完成 ✓ (%s)", ffmpegPath), 30)

		// ── Step 3: Clone and build RapidSpeech.cpp ──
		sendSSE("step", "正在克隆 RapidSpeech.cpp 仓库...", 35)
		if err := os.MkdirAll(baseDir, 0755); err != nil {
			sendSSE("error", fmt.Sprintf("创建目录失败 %s: %v", baseDir, err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		repoDir := filepath.Join(baseDir, "RapidSpeech.cpp")
		repoURL := "https://github.com/RapidAI/RapidSpeech.cpp"
		if isChinaRegion {
			// Use gitee mirror if available, fallback to github
			repoURL = "https://gitee.com/RapidAI/RapidSpeech.cpp"
			sendSSE("log", "检测到中国区域，使用 Gitee 镜像", -1)
		}

		if info, err := os.Stat(repoDir); err == nil && info.IsDir() {
			sendSSE("log", "仓库目录已存在，执行 git pull...", -1)
			if err := runCmd(ctx, "git", "-C", repoDir, "pull"); err != nil {
				sendSSE("log", "git pull 失败，将重新克隆...", -1)
				os.RemoveAll(repoDir)
				if err := runCmd(ctx, "git", "clone", "--depth=1", repoURL, repoDir); err != nil {
					sendSSE("error", fmt.Sprintf("克隆仓库失败: %v", err), -1)
					sendSSE("done", "安装失败", -1)
					return
				}
			}
		} else {
			if err := runCmd(ctx, "git", "clone", "--depth=1", repoURL, repoDir); err != nil {
				// If gitee failed, try github
				if isChinaRegion {
					sendSSE("log", "Gitee 克隆失败，尝试 GitHub...", -1)
					repoURL = "https://github.com/RapidAI/RapidSpeech.cpp"
					if err := runCmd(ctx, "git", "clone", "--depth=1", repoURL, repoDir); err != nil {
						sendSSE("error", fmt.Sprintf("克隆仓库失败: %v", err), -1)
						sendSSE("done", "安装失败", -1)
						return
					}
				} else {
					sendSSE("error", fmt.Sprintf("克隆仓库失败: %v", err), -1)
					sendSSE("done", "安装失败", -1)
					return
				}
			}
		}
		sendSSE("step", "仓库克隆完成 ✓", 45)

		// Init submodules
		sendSSE("step", "正在初始化子模块...", 48)
		runCmd(ctx, "git", "-C", repoDir, "submodule", "sync")
		if err := runCmd(ctx, "git", "-C", repoDir, "submodule", "update", "--init", "--recursive"); err != nil {
			sendSSE("error", fmt.Sprintf("子模块初始化失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		sendSSE("step", "子模块初始化完成 ✓", 52)

		// Build
		sendSSE("step", "正在编译 RapidSpeech.cpp (cmake)...", 55)
		buildDir := filepath.Join(repoDir, "build")
		if err := os.MkdirAll(buildDir, 0755); err != nil {
			sendSSE("error", fmt.Sprintf("创建 build 目录失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		if err := runCmd(ctx, "cmake", "-B", buildDir, "-S", repoDir, "-DCMAKE_BUILD_TYPE=Release"); err != nil {
			sendSSE("error", fmt.Sprintf("cmake 配置失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		sendSSE("step", "cmake 配置完成，开始编译...", 60)
		numCPU := runtime.NumCPU()
		if numCPU < 1 {
			numCPU = 1
		}
		if err := runCmd(ctx, "cmake", "--build", buildDir, "--config", "Release",
			fmt.Sprintf("-j%d", numCPU)); err != nil {
			sendSSE("error", fmt.Sprintf("编译失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		// Find the built binary
		rsPath := filepath.Join(buildDir, "rs-asr-offline")
		if _, err := os.Stat(rsPath); err != nil {
			rsPath = filepath.Join(buildDir, "examples", "rs-asr-offline")
			if _, err := os.Stat(rsPath); err != nil {
				sendSSE("error", "编译完成但未找到 rs-asr-offline 可执行文件", -1)
				sendSSE("done", "安装失败", -1)
				return
			}
		}
		os.Chmod(rsPath, 0755)
		sendSSE("step", fmt.Sprintf("RapidSpeech.cpp 编译完成 ✓ (%s)", rsPath), 70)

		// ── Step 4: Download model ──
		sendSSE("step", "正在下载 RapidSpeech 模型文件...", 75)
		modelSubDir := filepath.Join(modelDir, "RapidSpeech", "ASR", "SenseVoice")
		if err := os.MkdirAll(modelSubDir, 0755); err != nil {
			sendSSE("error", fmt.Sprintf("创建模型目录失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		modelFile := filepath.Join(modelSubDir, "sense-voice-small-q5_k.gguf")

		if _, err := os.Stat(modelFile); err == nil {
			sendSSE("log", "模型文件已存在，跳过下载", -1)
		} else {
			var modelURL string
			if isChinaRegion {
				modelURL = "https://www.modelscope.cn/models/RapidAI/RapidSpeech/resolve/master/ASR/SenseVoice/sense-voice-small-q5_k.gguf"
				sendSSE("log", "使用 ModelScope 下载模型...", -1)
			} else {
				modelURL = "https://huggingface.co/RapidAI/RapidSpeech/resolve/main/ASR/SenseVoice/sense-voice-small-q5_k.gguf"
				sendSSE("log", "使用 Hugging Face 下载模型...", -1)
			}
			if err := runCmd(ctx, "wget", "--progress=dot:mega", "-O", modelFile, modelURL); err != nil {
				// Fallback to the other source
				if isChinaRegion {
					sendSSE("log", "ModelScope 下载失败，尝试 Hugging Face...", -1)
					modelURL = "https://huggingface.co/RapidAI/RapidSpeech/resolve/main/ASR/SenseVoice/sense-voice-small-q5_k.gguf"
				} else {
					sendSSE("log", "Hugging Face 下载失败，尝试 ModelScope...", -1)
					modelURL = "https://www.modelscope.cn/models/RapidAI/RapidSpeech/resolve/master/ASR/SenseVoice/sense-voice-small-q5_k.gguf"
				}
				os.Remove(modelFile) // remove partial download
				if err := runCmd(ctx, "wget", "--progress=dot:mega", "-O", modelFile, modelURL); err != nil {
					sendSSE("error", fmt.Sprintf("模型下载失败: %v", err), -1)
					sendSSE("done", "安装失败", -1)
					return
				}
			}
		}
		sendSSE("step", fmt.Sprintf("模型下载完成 ✓ (%s)", modelFile), 88)

		// ── Step 5: Update config ──
		sendSSE("step", "正在更新系统配置...", 92)
		configUpdates := map[string]interface{}{
			"video.ffmpeg_path":       ffmpegPath,
			"video.rapidspeech_path":  rsPath,
			"video.rapidspeech_model": modelFile,
		}
		if err := app.configManager.Update(configUpdates); err != nil {
			sendSSE("error", fmt.Sprintf("配置更新失败: %v", err), -1)
			sendSSE("done", "安装失败", -1)
			return
		}
		sendSSE("step", "配置更新完成 ✓", 98)

		// ── Done ──
		sendSSE("done", "自动配置完成！FFmpeg 和 RapidSpeech 已安装并配置。", 100)
	}
}
